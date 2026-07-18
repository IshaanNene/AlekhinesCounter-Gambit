%%% Two-node handoff test: the flagship proof that a live game survives the death
%%% of the node that owned it. It spins up two real session-manager nodes sharing
%%% one Redis, plays a move on the game's owner, kills that node, and shows the
%%% game re-homing onto the survivor with its clock and turn intact.
%%%
%%% Needs both a Redis (ACG_REDIS_ADDR, for the checkpoint) and a working Erlang
%%% distribution; it skips cleanly when either is unavailable, so it never breaks
%%% a bare CI run. The single-node suites cover the logic; this covers the wiring.
-module(sm_dist_tests).
-include_lib("eunit/include/eunit.hrl").

handoff_test_() ->
    {timeout, 90, fun maybe_run/0}.

maybe_run() ->
    case os:getenv("ACG_REDIS_ADDR") of
        false ->
            ?debugMsg("sm_dist_tests: skipped (ACG_REDIS_ADDR unset)"),
            ok;
        Addr ->
            case ensure_distributed() of
                ok -> run(Addr);
                {skip, Why} -> ?debugFmt("SMDIST_SKIP no_distribution: ~p", [Why])
            end
    end.

run(Addr) ->
    Cookie = atom_to_list(erlang:get_cookie()),
    %% Unique per run so a leftover epmd registration from a previous run can
    %% never collide and silently turn this into a skip.
    Suffix = integer_to_list(erlang:unique_integer([positive])),
    case {start_peer("acga" ++ Suffix, Addr, Cookie), start_peer("acgb" ++ Suffix, Addr, Cookie)} of
        {{ok, PA, A}, {ok, PB, B}} ->
            try
                handoff(PA, A, PB, B)
            catch
                throw:{skip, Why} -> ?debugFmt("SMDIST_SKIP ~p", [Why])
            after
                catch peer:stop(PA),
                catch peer:stop(PB)
            end;
        Other ->
            ?debugFmt("SMDIST_SKIP peer_start_failed: ~p", [Other]),
            ok
    end.

handoff(PA, A, PB, B) ->
    %% Form the cluster and start the app on both nodes.
    case peer:call(PA, net_kernel, connect_node, [B]) of
        true -> ok;
        _ -> throw({skip, cannot_connect_nodes})
    end,
    ok = start_app(PA),
    ok = start_app(PB),
    wait_until(fun() -> members(PA) =:= 2 andalso members(PB) =:= 2 end),

    %% A game whose id is owned by node A under the two-node cluster.
    GameId = find_owned_by(PA, A),

    %% Create it (from B, to prove routing) and play White's move.
    {ok, _} = peer:call(PB, session_manager, create_game,
        [GameId, #{white => <<"w">>, black => <<"b">>, initial_ms => 600000}]),
    {ok, S1} = peer:call(PB, session_manager, move, [GameId, <<"w">>]),
    ?assertEqual(black, maps:get(turn, S1)),
    %% The live process really is on A.
    ?assertEqual(A, node(peer:call(PB, sm_registry, whereis_game, [GameId]))),

    %% Kill A hard — simulating a pod loss, not a graceful drain.
    catch peer:call(PA, erlang, halt, [0]),

    %% B must notice A leave and become the game's new owner.
    wait_until(fun() -> members(PB) =:= 1 andalso peer:call(PB, sm_cluster, owner, [GameId]) =:= B end),

    %% Black's move now lands on B: the game is gone from the registry, so the
    %% call re-homes it from the checkpoint and forwards. This is the whole point.
    {ok, S2} = peer:call(PB, session_manager, move, [GameId, <<"b">>]),
    ?assertEqual(white, maps:get(turn, S2)),
    ?assertEqual(in_progress, maps:get(status, S2)),
    %% The game now lives on B.
    ?assertEqual(B, node(peer:call(PB, sm_registry, whereis_game, [GameId]))),

    %% Clocks survived the handoff: both sides still hold most of their 10 minutes
    %% (only the brief outage was charged to the side that was on the move).
    #{white := WMs, black := BMs} = maps:get(time_left, S2),
    ?assert(WMs > 500000),
    ?assert(BMs > 500000),
    ?debugFmt("SMDIST_PASS handoff verified: game re-homed ~p -> ~p, clocks W=~pms B=~pms",
        [A, B, WMs, BMs]),
    ok.

%%% helpers

members(P) -> length(peer:call(P, sm_cluster, nodes_up, [])).

%% Start Erlang distribution on the test runner if it is not already distributed.
ensure_distributed() ->
    case node() of
        'nonode@nohost' ->
            Name = list_to_atom("acg_ct_" ++ integer_to_list(erlang:unique_integer([positive])) ++ "@127.0.0.1"),
            case net_kernel:start([Name, longnames]) of
                {ok, _} ->
                    erlang:set_cookie(node(), 'acg_dist_test'),
                    ok;
                {error, Reason} ->
                    {skip, {net_kernel, Reason}}
            end;
        _ ->
            ok
    end.

start_peer(Name, Addr, Cookie) ->
    Paths = lists:flatmap(fun(P) -> ["-pa", P] end, code:get_path()),
    peer:start(#{
        name => Name,
        host => "127.0.0.1",
        longnames => true,
        connection => standard_io,
        env => [{"ACG_REDIS_ADDR", Addr}],
        args => ["-setcookie", Cookie | Paths],
        wait_boot => 30000
    }).

start_app(P) ->
    %% No gRPC listener in the test: we drive the API directly across nodes.
    ok = peer:call(P, application, load, [session_manager]),
    ok = peer:call(P, application, set_env, [session_manager, start_grpc, false]),
    {ok, _} = peer:call(P, application, ensure_all_started, [session_manager]),
    ok.

find_owned_by(P, Node) -> find_owned_by(P, Node, 1).

find_owned_by(P, Node, N) when N < 5000 ->
    Id = list_to_binary("dist-" ++ integer_to_list(N)),
    case peer:call(P, sm_cluster, owner, [Id]) of
        Node -> Id;
        _ -> find_owned_by(P, Node, N + 1)
    end.

wait_until(Fun) -> wait_until(Fun, 100).

wait_until(_Fun, 0) -> error(timeout_waiting_for_condition);
wait_until(Fun, N) ->
    case (catch Fun()) of
        true -> ok;
        _ -> timer:sleep(100), wait_until(Fun, N - 1)
    end.
