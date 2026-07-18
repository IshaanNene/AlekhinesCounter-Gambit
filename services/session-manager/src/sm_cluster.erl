%%% @doc Cluster membership and game ownership.
%%%
%%% Two concerns:
%%%
%%%   1. Keep this node connected to its peers, so syn and Erlang distribution
%%%      form one cluster. Peers come from ACG_CLUSTER_PEERS (a comma-separated
%%%      list of node names) and are (re)connected on a timer, which also heals a
%%%      transient partition.
%%%
%%%   2. Decide which node *owns* a given game. Ownership uses rendezvous
%%%      (highest-random-weight) hashing rather than a modulo ring: when a node
%%%      joins or leaves, only the games that actually hash to/from it move, so a
%%%      membership change reshuffles the minimum possible set of live games.
%%%
%%% Ownership is a pure function of the current node set, so every node computes
%%% the same owner for a game without any coordination.
-module(sm_cluster).
-behaviour(gen_server).

-export([start_link/0, owner/1, owner/2, nodes_up/0, is_owner/1]).
-export([init/1, handle_call/3, handle_cast/2, handle_info/2, terminate/2]).

-define(RECONNECT_INTERVAL, 5000).

%%% API

start_link() ->
    gen_server:start_link({local, ?MODULE}, ?MODULE, [], []).

%% @doc The live nodes eligible to own games: the session-manager members of the
%% cluster (not every connected node), so ownership is stable and never lands a
%% game on a node that is merely attached (a remote shell, tooling).
-spec nodes_up() -> [node()].
nodes_up() ->
    sm_registry:member_nodes().

%% @doc The node that owns GameId under the current membership. Rendezvous
%% hashing: the owner is the node scoring the highest hash of {GameId, Node}.
-spec owner(binary()) -> node().
owner(GameId) ->
    owner(GameId, nodes_up()).

%% @doc Whether this node owns GameId right now.
-spec is_owner(binary()) -> boolean().
is_owner(GameId) ->
    owner(GameId) =:= node().

%%% gen_server callbacks

init([]) ->
    %% Announce this node as a session-manager member (sm_registry has already
    %% added the syn scope). Connect to peers eagerly, then on a timer; a peer not
    %% up yet is simply retried, and a healed partition reconnects the same way.
    ok = sm_registry:join_cluster(self()),
    self() ! reconnect,
    {ok, #{}}.

handle_call(_Request, _From, State) ->
    {reply, {error, unknown_request}, State}.

handle_cast(_Msg, State) ->
    {noreply, State}.

handle_info(reconnect, State) ->
    %% Discover peers afresh each tick, so pods that come and go (and a headless
    %% Service whose A records change) are picked up without a restart.
    _ = [net_kernel:connect_node(N) || N <- discover_peers(), N =/= node()],
    erlang:send_after(?RECONNECT_INTERVAL, self(), reconnect),
    {noreply, State};
handle_info(_Info, State) ->
    {noreply, State}.

terminate(_Reason, _State) ->
    ok.

%%% internal

%% Peers come from two optional sources, unioned: an explicit ACG_CLUSTER_PEERS
%% list (handy for a fixed cluster or local testing) and ACG_CLUSTER_DNS, a
%% headless-Service name whose A records are every session-manager pod IP. The
%% latter is the k8s-native path: no replica count baked in anywhere.
discover_peers() ->
    lists:usort(static_peers() ++ dns_peers()).

static_peers() ->
    case os:getenv("ACG_CLUSTER_PEERS") of
        false -> [];
        "" -> [];
        Str -> [list_to_atom(string:trim(P)) || P <- string:split(Str, ",", all), P =/= ""]
    end.

dns_peers() ->
    case os:getenv("ACG_CLUSTER_DNS") of
        false -> [];
        "" -> [];
        Name ->
            Base = node_basename(),
            [peer_node(Base, Ip) || Ip <- inet_res:lookup(Name, in, a)]
    end.

%% The name part of our own node ("session_manager" from session_manager@10.1.2.3)
%% so discovered peers share it.
node_basename() ->
    case string:split(atom_to_list(node()), "@") of
        [Base, _Host] -> Base;
        [Base] -> Base
    end.

peer_node(Base, Ip) ->
    list_to_atom(Base ++ "@" ++ inet:ntoa(Ip)).

%% @doc Owner of GameId within an explicit node set — the pure core of ownership,
%% exported so it is testable against an arbitrary cluster without a real one.
-spec owner(binary(), [node()]) -> node() | undefined.
owner(_GameId, []) ->
    undefined;
owner(GameId, Nodes) ->
    {_Weight, Owner} = lists:max([{weight(GameId, N), N} || N <- Nodes]),
    Owner.

%% weight scores a (game, node) pair. phash2 is stable across nodes and OTP
%% versions, so every node agrees on the ranking.
weight(GameId, Node) ->
    erlang:phash2({GameId, Node}).

%% configured_peers reads ACG_CLUSTER_PEERS ("a@host,b@host") into node atoms.
configured_peers() ->
    case os:getenv("ACG_CLUSTER_PEERS") of
        false -> [];
        "" -> [];
        Str -> [list_to_atom(string:trim(P)) || P <- string:split(Str, ",", all), P =/= ""]
    end.
