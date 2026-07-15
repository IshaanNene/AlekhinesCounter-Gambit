%%% @doc Game registry: maps a game id to the pid of its game process.
%%%
%%% Backed by a protected named ETS table so lookups are lock-free reads from any
%%% process. The registry monitors each registered process and removes its entry
%%% automatically when it dies, so stale ids never linger.
-module(sm_registry).
-behaviour(gen_server).

-export([start_link/0, register/2, unregister/1, whereis_game/1, all/0]).
-export([init/1, handle_call/3, handle_cast/2, handle_info/2, terminate/2]).

-define(TAB, sm_registry_tab).

%%% API

start_link() ->
    gen_server:start_link({local, ?MODULE}, ?MODULE, [], []).

-spec register(binary(), pid()) -> ok | {error, already_registered}.
register(GameId, Pid) ->
    gen_server:call(?MODULE, {register, GameId, Pid}).

-spec unregister(binary()) -> ok.
unregister(GameId) ->
    gen_server:call(?MODULE, {unregister, GameId}).

%% @doc Fast, direct ETS read (no gen_server round-trip).
-spec whereis_game(binary()) -> pid() | undefined.
whereis_game(GameId) ->
    case ets:lookup(?TAB, GameId) of
        [{GameId, Pid}] -> Pid;
        [] -> undefined
    end.

-spec all() -> [binary()].
all() ->
    [GameId || {GameId, _Pid} <- ets:tab2list(?TAB)].

%%% gen_server callbacks

init([]) ->
    ?TAB = ets:new(?TAB, [named_table, protected, set, {read_concurrency, true}]),
    %% State maps monitor refs to game ids so DOWN messages can clean up.
    {ok, #{mons => #{}}}.

handle_call({register, GameId, Pid}, _From, State = #{mons := Mons}) ->
    case ets:lookup(?TAB, GameId) of
        [{GameId, _Existing}] ->
            {reply, {error, already_registered}, State};
        [] ->
            Ref = erlang:monitor(process, Pid),
            true = ets:insert(?TAB, {GameId, Pid}),
            {reply, ok, State#{mons := Mons#{Ref => GameId}}}
    end;
handle_call({unregister, GameId}, _From, State) ->
    true = ets:delete(?TAB, GameId),
    {reply, ok, State};
handle_call(_Request, _From, State) ->
    {reply, {error, unknown_request}, State}.

handle_cast(_Msg, State) ->
    {noreply, State}.

handle_info({'DOWN', Ref, process, _Pid, _Reason}, State = #{mons := Mons}) ->
    case maps:take(Ref, Mons) of
        {GameId, Mons2} ->
            true = ets:delete(?TAB, GameId),
            {noreply, State#{mons := Mons2}};
        error ->
            {noreply, State}
    end;
handle_info(_Info, State) ->
    {noreply, State}.

terminate(_Reason, _State) ->
    ok.
