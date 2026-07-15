%%% @doc gRPC facade over the session manager: implements
%%% `alekhine.session.v1.SessionService' so the Go game-service and gateway can
%%% drive live sessions across the language boundary.
%%%
%%% Error convention: an unknown game id is a gRPC NOT_FOUND. Domain rejections
%%% that are meaningful to the caller (`not_your_turn', `not_a_player',
%%% `game_over') are *not* transport errors — they return the current snapshot
%%% with the `error' field set, so callers still learn the live clock/turn state.
-module(sm_grpc_service).
-behaviour(alekhine_session_v_1_session_service_bhvr).

-include_lib("grpcbox/include/grpcbox.hrl").

-export([
    create_session/2,
    join/2,
    move_made/2,
    disconnect/2,
    reconnect/2,
    resign/2,
    end_session/2,
    get_snapshot/2
]).

-define(DEFAULT_INITIAL_MS, 300000).
-define(DEFAULT_INCREMENT_MS, 0).
-define(DEFAULT_GRACE_MS, 30000).

create_session(Ctx, Req) ->
    GameId = maps:get(game_id, Req, <<>>),
    Opts = #{
        white => empty_to_undefined(maps:get(white_id, Req, <<>>)),
        black => empty_to_undefined(maps:get(black_id, Req, <<>>)),
        initial_ms => default_if_zero(maps:get(initial_ms, Req, 0), ?DEFAULT_INITIAL_MS),
        increment_ms => default_if_zero(maps:get(increment_ms, Req, 0), ?DEFAULT_INCREMENT_MS),
        grace_ms => default_if_zero(maps:get(grace_ms, Req, 0), ?DEFAULT_GRACE_MS)
    },
    case session_manager:create_game(GameId, Opts) of
        {ok, _Pid} ->
            snapshot_reply(Ctx, GameId, <<>>);
        {error, {already_started, _}} ->
            already_exists(GameId);
        {error, already_registered} ->
            already_exists(GameId);
        {error, Reason} ->
            {grpc_error, {?GRPC_STATUS_INTERNAL, to_bin(Reason)}}
    end.

join(Ctx, Req) ->
    with_player(Ctx, Req, fun session_manager:join/2).

move_made(Ctx, Req) ->
    with_player(Ctx, Req, fun session_manager:move/2).

disconnect(Ctx, Req) ->
    with_player(Ctx, Req, fun session_manager:disconnect/2).

reconnect(Ctx, Req) ->
    with_player(Ctx, Req, fun session_manager:reconnect/2).

resign(Ctx, Req) ->
    with_player(Ctx, Req, fun session_manager:resign/2).

end_session(Ctx, Req) ->
    GameId = maps:get(game_id, Req, <<>>),
    Winner = side_from_pb(maps:get(winner, Req, 'SIDE_UNSPECIFIED')),
    Reason = maps:get(reason, Req, <<"ended">>),
    case session_manager:end_game(GameId, Winner, Reason) of
        {ok, Snap} -> {ok, to_pb(Snap, <<>>), Ctx};
        {error, no_such_game} -> not_found(GameId);
        {error, Reason2} -> snapshot_reply(Ctx, GameId, to_bin(Reason2))
    end.

get_snapshot(Ctx, Req) ->
    snapshot_reply(Ctx, maps:get(game_id, Req, <<>>), <<>>).

%%% internal

%% with_player applies a session_manager call taking {GameId, PlayerId} and maps
%% the result onto a Snapshot response.
with_player(Ctx, Req, Fun) ->
    GameId = maps:get(game_id, Req, <<>>),
    PlayerId = maps:get(player_id, Req, <<>>),
    case Fun(GameId, PlayerId) of
        {ok, Snap} ->
            {ok, to_pb(Snap, <<>>), Ctx};
        ok ->
            snapshot_reply(Ctx, GameId, <<>>);
        {error, no_such_game} ->
            not_found(GameId);
        {error, Reason} ->
            %% Domain rejection: surface it alongside the live state.
            snapshot_reply(Ctx, GameId, to_bin(Reason))
    end.

snapshot_reply(Ctx, GameId, Err) ->
    case session_manager:snapshot(GameId) of
        {error, no_such_game} -> not_found(GameId);
        Snap when is_map(Snap) -> {ok, to_pb(Snap, Err), Ctx}
    end.

not_found(GameId) ->
    {grpc_error, {?GRPC_STATUS_NOT_FOUND, <<"no such game: ", GameId/binary>>}}.

already_exists(GameId) ->
    {grpc_error, {?GRPC_STATUS_ALREADY_EXISTS, <<"session already exists: ", GameId/binary>>}}.

%% to_pb converts a session_manager snapshot map into the protobuf Snapshot map.
to_pb(Snap, Err) ->
    #{
        game_id := GameId,
        turn := Turn,
        status := Status,
        result := Result,
        spectators := Spectators,
        time_left := #{white := WhiteMs, black := BlackMs}
    } = Snap,
    {Winner, Reason} = result_to_pb(Result),
    #{
        game_id => GameId,
        status => status_to_pb(Status),
        turn => side_to_pb(Turn),
        white_ms => WhiteMs,
        black_ms => BlackMs,
        winner => Winner,
        reason => Reason,
        spectators => [to_bin(S) || S <- Spectators],
        error => Err
    }.

status_to_pb(in_progress) -> 'SESSION_STATUS_IN_PROGRESS';
status_to_pb(finished) -> 'SESSION_STATUS_FINISHED'.

side_to_pb(white) -> 'SIDE_WHITE';
side_to_pb(black) -> 'SIDE_BLACK'.

%% A draw arrives as SIDE_UNSPECIFIED and maps to `none' (no winner).
side_from_pb('SIDE_WHITE') -> white;
side_from_pb('SIDE_BLACK') -> black;
side_from_pb(_) -> none.

result_to_pb(undefined) ->
    {'SIDE_UNSPECIFIED', <<>>};
result_to_pb(#{winner := none, reason := Reason}) ->
    {'SIDE_UNSPECIFIED', to_bin(Reason)};
result_to_pb(#{winner := Winner, reason := Reason}) ->
    {side_to_pb(Winner), to_bin(Reason)}.

empty_to_undefined(<<>>) -> undefined;
empty_to_undefined(V) -> V.

default_if_zero(0, Default) -> Default;
default_if_zero(V, _Default) -> V.

to_bin(V) when is_binary(V) -> V;
to_bin(V) when is_atom(V) -> atom_to_binary(V, utf8);
to_bin(V) when is_list(V) -> list_to_binary(V);
to_bin(V) -> list_to_binary(io_lib:format("~p", [V])).
