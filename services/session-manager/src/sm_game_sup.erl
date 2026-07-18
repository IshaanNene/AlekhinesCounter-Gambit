%%% @doc Dynamic supervisor owning one process per live game.
%%%
%%% Uses simple_one_for_one so games can be started on demand. Children are
%%% `temporary': a live game holds ephemeral session state (clocks, presence)
%%% that a blind restart could not reconstruct, so on crash the session is
%%% dropped rather than silently resurrected with wrong state. The authoritative
%%% game record lives in the game-service/Postgres and can be replayed to spawn a
%%% fresh session if needed. Crucially, a crash in one game never affects another.
-module(sm_game_sup).
-behaviour(supervisor).

-export([start_link/0, start_game/2, ensure_game/1, init/1]).

start_link() ->
    supervisor:start_link({local, ?MODULE}, ?MODULE, []).

-spec start_game(binary(), map()) -> {ok, pid()} | {error, term()}.
start_game(GameId, Opts) ->
    supervisor:start_child(?MODULE, [GameId, Opts]).

%% @doc Re-home a game onto this node from its durable checkpoint, if one exists.
%% Called on the game's (new) owner after the previous owner died, so a live game
%% survives a node loss. Returns {error, no_such_game} when there is no checkpoint
%% to restore from (the game never existed, or has expired).
-spec ensure_game(binary()) -> {ok, pid()} | {error, term()}.
ensure_game(GameId) ->
    %% The checkpoint is decoded with binary_to_term/[safe], which refuses to
    %% invent atoms that do not already exist. All of a checkpoint's atoms live in
    %% sm_game, which is loaded lazily — so on a node that has not started a game
    %% yet, force it loaded before decoding, or the very first restore fails.
    _ = code:ensure_loaded(sm_game),
    case sm_checkpoint:load(GameId) of
        {ok, CP} -> supervisor:start_child(?MODULE, [GameId, #{restore => CP}]);
        none -> {error, no_such_game};
        {error, _} = Err -> Err
    end.

init([]) ->
    SupFlags = #{strategy => simple_one_for_one, intensity => 10, period => 10},
    ChildSpec = #{
        id => sm_game,
        start => {sm_game, start_link, []},
        restart => temporary,
        shutdown => 5000,
        type => worker
    },
    {ok, {SupFlags, [ChildSpec]}}.
