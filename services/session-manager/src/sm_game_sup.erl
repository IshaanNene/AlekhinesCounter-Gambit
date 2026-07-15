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

-export([start_link/0, start_game/2, init/1]).

start_link() ->
    supervisor:start_link({local, ?MODULE}, ?MODULE, []).

-spec start_game(binary(), map()) -> {ok, pid()} | {error, term()}.
start_game(GameId, Opts) ->
    supervisor:start_child(?MODULE, [GameId, Opts]).

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
