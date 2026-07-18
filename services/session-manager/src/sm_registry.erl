%%% @doc Cluster-wide game registry: maps a game id to the pid of its game
%%% process, wherever in the cluster that process lives.
%%%
%%% Backed by `syn', an eventually-consistent distributed registry. This is what
%%% lets the session tier run more than one replica: a game created on node A is
%%% addressable from node B, so `gen_server:call' (which is location-transparent)
%%% reaches it over Erlang distribution without the caller knowing or caring which
%%% node owns it. syn also monitors every registered process and drops its entry
%%% automatically when it (or its whole node) dies.
%%%
%%% The module keeps its historical API — register/2, unregister/1,
%%% whereis_game/1, all/0 — so callers are unchanged from the single-node era.
-module(sm_registry).
-behaviour(gen_server).

-export([start_link/0, register/2, unregister/1, whereis_game/1, all/0]).
-export([join_cluster/1, member_nodes/0]).
-export([init/1, handle_call/3, handle_cast/2, handle_info/2, terminate/2]).

%% One syn scope carries the id->pid registry, the `all_games' group used to
%% enumerate live games, and the `sm_nodes' group of session-manager nodes.
-define(SCOPE, acg_games).
-define(ALL_GROUP, all_games).
-define(NODES_GROUP, sm_nodes).

%%% API

start_link() ->
    gen_server:start_link({local, ?MODULE}, ?MODULE, [], []).

%% @doc Register a game process cluster-wide. Returns {error, already_registered}
%% if the id is already taken, matching the old ETS-based contract sm_game relies
%% on to refuse a duplicate.
-spec register(binary(), pid()) -> ok | {error, already_registered}.
register(GameId, Pid) ->
    case syn:register(?SCOPE, GameId, Pid) of
        ok ->
            %% Join a group keyed by the same pid so all/0 can list live games;
            %% syn removes the membership when the pid dies, same as the registry.
            _ = syn:join(?SCOPE, ?ALL_GROUP, Pid, GameId),
            ok;
        {error, taken} ->
            {error, already_registered}
    end.

-spec unregister(binary()) -> ok.
unregister(GameId) ->
    _ = syn:unregister(?SCOPE, GameId),
    ok.

%% @doc Resolve a game id to its process pid anywhere in the cluster, or
%% `undefined' if no node currently hosts it.
-spec whereis_game(binary()) -> pid() | undefined.
whereis_game(GameId) ->
    case syn:lookup(?SCOPE, GameId) of
        {Pid, _Meta} -> Pid;
        undefined -> undefined
    end.

-spec all() -> [binary()].
all() ->
    lists:usort([Meta || {_Pid, Meta} <- syn:members(?SCOPE, ?ALL_GROUP)]).

%% @doc Announce this node as a session-manager member of the cluster, using Pid
%% (a long-lived per-node process) as the marker. syn drops the membership when
%% the node dies, which is how ownership sees a node leave.
-spec join_cluster(pid()) -> ok.
join_cluster(Pid) ->
    _ = syn:join(?SCOPE, ?NODES_GROUP, Pid, node()),
    ok.

%% @doc The session-manager nodes currently in the cluster. Ownership is computed
%% over exactly this set, so an unrelated connected node (a remote shell, a
%% tooling node) never gets handed games. Falls back to this node alone before
%% the group has propagated.
-spec member_nodes() -> [node()].
member_nodes() ->
    case syn:members(?SCOPE, ?NODES_GROUP) of
        [] -> [node()];
        Members -> lists:usort([node(Pid) || {Pid, _Meta} <- Members])
    end.

%%% gen_server callbacks
%%%
%%% The process exists only to add this node to the syn scope for the lifetime of
%%% the application (scopes are per-node, not per-process, so one call suffices).

init([]) ->
    ok = syn:add_node_to_scopes([?SCOPE]),
    {ok, #{}}.

handle_call(_Request, _From, State) ->
    {reply, {error, unknown_request}, State}.

handle_cast(_Msg, State) ->
    {noreply, State}.

handle_info(_Info, State) ->
    {noreply, State}.

terminate(_Reason, _State) ->
    ok.
