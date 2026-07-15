-module(sm_game_tests).
-include_lib("eunit/include/eunit.hrl").

game_test_() ->
    {foreach, fun setup/0, fun cleanup/1, [
        fun move_flow/1,
        fun rejects_non_player/1,
        fun resignation/1,
        fun flag_fall/1,
        fun abandonment_after_grace/1,
        fun reconnect_cancels_grace/1,
        fun end_game_stops_clocks/1,
        fun crash_isolation/1
    ]}.

setup() ->
    %% Unit tests exercise the OTP logic directly; no gRPC listener needed.
    %% load/1 returns {error, {already_loaded, _}} on repeat fixtures — ignore it.
    _ = application:load(session_manager),
    ok = application:set_env(session_manager, start_grpc, false),
    {ok, _Started} = application:ensure_all_started(session_manager),
    ok.

cleanup(_) ->
    application:stop(session_manager).

%% Turns alternate; clock switches; wrong-turn moves are rejected.
move_flow(_) ->
    fun() ->
        {ok, _} = session_manager:create_game(<<"g">>,
            #{white => <<"w">>, black => <<"b">>, initial_ms => 60000}),
        Snap0 = session_manager:snapshot(<<"g">>),
        ?assertEqual(white, maps:get(turn, Snap0)),

        {ok, Snap1} = session_manager:move(<<"g">>, <<"w">>),
        ?assertEqual(black, maps:get(turn, Snap1)),

        ?assertEqual({error, not_your_turn}, session_manager:move(<<"g">>, <<"w">>)),

        {ok, Snap2} = session_manager:move(<<"g">>, <<"b">>),
        ?assertEqual(white, maps:get(turn, Snap2))
    end.

rejects_non_player(_) ->
    fun() ->
        {ok, _} = session_manager:create_game(<<"g">>,
            #{white => <<"w">>, black => <<"b">>}),
        ?assertEqual({error, not_a_player}, session_manager:move(<<"g">>, <<"stranger">>))
    end.

resignation(_) ->
    fun() ->
        {ok, _} = session_manager:create_game(<<"g">>,
            #{white => <<"w">>, black => <<"b">>}),
        {ok, Snap} = session_manager:resign(<<"g">>, <<"w">>),
        ?assertEqual(finished, maps:get(status, Snap)),
        ?assertEqual(#{winner => black, reason => resignation}, maps:get(result, Snap)),
        %% A finished game rejects further moves.
        ?assertEqual({error, game_over}, session_manager:move(<<"g">>, <<"b">>))
    end.

%% With a tiny initial time and no moves, white flags and black wins on time.
flag_fall(_) ->
    fun() ->
        {ok, _} = session_manager:create_game(<<"g">>,
            #{white => <<"w">>, black => <<"b">>, initial_ms => 50}),
        timer:sleep(250),
        Snap = session_manager:snapshot(<<"g">>),
        ?assertEqual(finished, maps:get(status, Snap)),
        ?assertEqual(#{winner => black, reason => flag}, maps:get(result, Snap))
    end.

%% A disconnect that outlasts the grace window is adjudicated as abandonment.
abandonment_after_grace(_) ->
    fun() ->
        {ok, _} = session_manager:create_game(<<"g">>,
            #{white => <<"w">>, black => <<"b">>, initial_ms => 600000, grace_ms => 50}),
        ok = session_manager:disconnect(<<"g">>, <<"w">>),
        timer:sleep(250),
        Snap = session_manager:snapshot(<<"g">>),
        ?assertEqual(finished, maps:get(status, Snap)),
        ?assertEqual(#{winner => black, reason => abandonment}, maps:get(result, Snap))
    end.

%% Reconnecting within the grace window keeps the game alive.
reconnect_cancels_grace(_) ->
    fun() ->
        {ok, _} = session_manager:create_game(<<"g">>,
            #{white => <<"w">>, black => <<"b">>, initial_ms => 600000, grace_ms => 150}),
        ok = session_manager:disconnect(<<"g">>, <<"w">>),
        timer:sleep(40),
        ok = session_manager:reconnect(<<"g">>, <<"w">>),
        timer:sleep(200),
        Snap = session_manager:snapshot(<<"g">>),
        ?assertEqual(in_progress, maps:get(status, Snap))
    end.

%% A chess-level termination reported by the game-service must finish the
%% session and cancel its flag timer, so a decided game can never flag-fall.
end_game_stops_clocks(_) ->
    fun() ->
        {ok, _} = session_manager:create_game(<<"g">>,
            #{white => <<"w">>, black => <<"b">>, initial_ms => 60}),
        {ok, Snap} = session_manager:end_game(<<"g">>, white, <<"checkmate">>),
        ?assertEqual(finished, maps:get(status, Snap)),
        ?assertEqual(#{winner => white, reason => <<"checkmate">>}, maps:get(result, Snap)),

        %% Well past the 60ms clock: the flag timer must not overwrite the result.
        timer:sleep(250),
        After = session_manager:snapshot(<<"g">>),
        ?assertEqual(finished, maps:get(status, After)),
        ?assertEqual(#{winner => white, reason => <<"checkmate">>}, maps:get(result, After))
    end.

%% Killing one game process must not disturb another, and the registry must
%% drop the dead game automatically.
crash_isolation(_) ->
    fun() ->
        {ok, _} = session_manager:create_game(<<"iso_a">>,
            #{white => <<"w">>, black => <<"b">>}),
        {ok, _} = session_manager:create_game(<<"iso_b">>,
            #{white => <<"w2">>, black => <<"b2">>}),

        PidA = sm_registry:whereis_game(<<"iso_a">>),
        ?assert(is_pid(PidA)),
        MRef = erlang:monitor(process, PidA),
        exit(PidA, kill),
        receive
            {'DOWN', MRef, process, PidA, _} -> ok
        after 1000 ->
            error(process_did_not_die)
        end,
        timer:sleep(50), %% let the registry process the DOWN

        ?assertEqual(undefined, sm_registry:whereis_game(<<"iso_a">>)),
        ?assertMatch(#{status := in_progress}, session_manager:snapshot(<<"iso_b">>)),
        ?assert(lists:member(<<"iso_b">>, session_manager:list_games())),
        ?assertNot(lists:member(<<"iso_a">>, session_manager:list_games()))
    end.
