-module(sm_checkpoint_tests).
-include_lib("eunit/include/eunit.hrl").

%% These exercise the real Redis path. Like the Go integration tests, they skip
%% when ACG_REDIS_ADDR is unset, so the suite stays green with no infrastructure.

checkpoint_test_() ->
    case os:getenv("ACG_REDIS_ADDR") of
        false ->
            {"skipped (ACG_REDIS_ADDR unset)", fun() -> ok end};
        _ ->
            {setup, fun setup/0, fun cleanup/1, fun(_) -> [
                fun save_load_roundtrip/0,
                fun load_missing_is_none/0,
                fun delete_removes/0
            ] end}
    end.

setup() ->
    {ok, _} = application:ensure_all_started(eredis),
    {ok, Pid} = sm_checkpoint:start_link(),
    Pid.

cleanup(Pid) ->
    _ = sm_checkpoint:delete(<<"cp-test">>),
    gen_server:stop(Pid).

sample() ->
    #{
        game_id => <<"cp-test">>,
        white => <<"w">>, black => <<"b">>,
        increment_ms => 2000, grace_ms => 30000,
        white_ms => 45000, black_ms => 51000,
        turn => black, status => in_progress, result => undefined,
        checkpoint_at => erlang:system_time(millisecond)
    }.

save_load_roundtrip() ->
    ?assert(sm_checkpoint:enabled()),
    ok = sm_checkpoint:save(sample()),
    ?assertEqual({ok, sample_matches()}, normalize(sm_checkpoint:load(<<"cp-test">>))).

%% The checkpoint_at timestamp differs per call, so compare the stable fields.
sample_matches() ->
    maps:remove(checkpoint_at, sample()).

normalize({ok, Map}) -> {ok, maps:remove(checkpoint_at, Map)};
normalize(Other) -> Other.

load_missing_is_none() ->
    ?assertEqual(none, sm_checkpoint:load(<<"definitely-absent">>)).

delete_removes() ->
    ok = sm_checkpoint:save(sample()),
    ok = sm_checkpoint:delete(<<"cp-test">>),
    ?assertEqual(none, sm_checkpoint:load(<<"cp-test">>)).
