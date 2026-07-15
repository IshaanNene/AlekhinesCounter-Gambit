-module(sm_clock_tests).
-include_lib("eunit/include/eunit.hrl").

new_initializes_both_sides_test() ->
    C = sm_clock:new(1000, 100),
    ?assertEqual({1000, 1000}, sm_clock:remaining(C)),
    ?assertEqual(none, sm_clock:running(C)).

start_sets_running_side_test() ->
    C = sm_clock:start(sm_clock:new(1000, 0), white, 0),
    ?assertEqual(white, sm_clock:running(C)).

on_move_deducts_elapsed_and_adds_increment_test() ->
    C0 = sm_clock:start(sm_clock:new(1000, 100), white, 0),
    {ok, C1} = sm_clock:on_move(C0, 300),
    %% White used 300ms, gains 100ms increment => 800ms; black's clock now runs.
    ?assertEqual({800, 1000}, sm_clock:remaining(C1)),
    ?assertEqual(black, sm_clock:running(C1)).

on_move_flags_when_time_exhausted_test() ->
    C0 = sm_clock:start(sm_clock:new(1000, 100), white, 0),
    ?assertEqual({flag, white}, sm_clock:on_move(C0, 1500)).

on_move_with_no_running_clock_is_noop_test() ->
    C = sm_clock:new(1000, 0),
    ?assertEqual({ok, C}, sm_clock:on_move(C, 500)).

time_left_accounts_for_running_clock_test() ->
    C = sm_clock:start(sm_clock:new(1000, 0), white, 0),
    ?assertEqual(700, sm_clock:time_left(C, white, 300)),
    %% The idle side is unaffected by elapsed time.
    ?assertEqual(1000, sm_clock:time_left(C, black, 300)).
