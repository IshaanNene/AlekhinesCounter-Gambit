%%% @doc Public API facade for the session manager. Callers address games by
%%% their game id; this module resolves the id to a process via {@link
%%% sm_registry} and forwards the request.
-module(session_manager).

-export([
    create_game/2,
    join/2,
    watch/2,
    unwatch/2,
    move/2,
    disconnect/2,
    reconnect/2,
    resign/2,
    end_game/3,
    snapshot/1,
    list_games/0
]).

%% @doc Start a new live game.
%%
%% Opts keys (all optional): `white' and `black' player ids, `initial_ms'
%% (default 300000), `increment_ms' (default 0), `grace_ms' (default 30000).
-spec create_game(binary(), map()) -> {ok, pid()} | {error, term()}.
create_game(GameId, Opts) ->
    sm_game_sup:start_game(GameId, Opts).

-spec join(binary(), term()) -> ok | {error, term()}.
join(GameId, PlayerId) -> call(GameId, {join, PlayerId}).

-spec watch(binary(), term()) -> ok | {error, term()}.
watch(GameId, SpectatorId) -> call(GameId, {watch, SpectatorId}).

-spec unwatch(binary(), term()) -> ok | {error, term()}.
unwatch(GameId, SpectatorId) -> call(GameId, {unwatch, SpectatorId}).

-spec move(binary(), term()) -> {ok, map()} | {error, term()}.
move(GameId, PlayerId) -> call(GameId, {move, PlayerId}).

-spec disconnect(binary(), term()) -> ok | {error, term()}.
disconnect(GameId, PlayerId) -> call(GameId, {disconnect, PlayerId}).

-spec reconnect(binary(), term()) -> ok | {error, term()}.
reconnect(GameId, PlayerId) -> call(GameId, {reconnect, PlayerId}).

-spec resign(binary(), term()) -> {ok, map()} | {error, term()}.
resign(GameId, PlayerId) -> call(GameId, {resign, PlayerId}).

%% @doc End a game for a chess-level reason the session cannot observe itself
%% (checkmate, stalemate, draw). Winner is `white', `black', or `none'.
-spec end_game(binary(), white | black | none, atom() | binary()) ->
    {ok, map()} | {error, term()}.
end_game(GameId, Winner, Reason) -> call(GameId, {end_game, Winner, Reason}).

-spec snapshot(binary()) -> map() | {error, term()}.
snapshot(GameId) -> call(GameId, snapshot).

-spec list_games() -> [binary()].
list_games() -> sm_registry:all().

%%% internal

call(GameId, Msg) ->
    case sm_registry:whereis_game(GameId) of
        undefined -> {error, no_such_game};
        Pid -> gen_server:call(Pid, Msg)
    end.
