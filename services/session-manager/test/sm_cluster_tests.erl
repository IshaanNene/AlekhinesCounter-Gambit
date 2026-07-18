-module(sm_cluster_tests).
-include_lib("eunit/include/eunit.hrl").

-define(NODES, ['a@h', 'b@h', 'c@h', 'd@h']).

ids() ->
    [list_to_binary("game-" ++ integer_to_list(N)) || N <- lists:seq(1, 500)].

%% Ownership is a pure, deterministic function of the id and node set.
deterministic_test() ->
    [?assertEqual(sm_cluster:owner(Id, ?NODES), sm_cluster:owner(Id, ?NODES)) || Id <- ids()],
    ok.

%% Every game maps to a node that is actually in the set.
owner_in_set_test() ->
    [?assert(lists:member(sm_cluster:owner(Id, ?NODES), ?NODES)) || Id <- ids()],
    ok.

empty_cluster_test() ->
    ?assertEqual(undefined, sm_cluster:owner(<<"g">>, [])).

single_node_test() ->
    [?assertEqual('only@h', sm_cluster:owner(Id, ['only@h'])) || Id <- ids()],
    ok.

%% The rendezvous-hashing property that justifies the choice: when a node leaves,
%% only the games it owned move; every other game keeps its owner. A modulo ring
%% would reshuffle almost everything, so this is the whole point.
minimal_reshuffle_on_node_loss_test() ->
    Gone = 'c@h',
    Remaining = ?NODES -- [Gone],
    Moved = [Id || Id <- ids(),
        sm_cluster:owner(Id, ?NODES) =/= sm_cluster:owner(Id, Remaining)],
    %% Everything that moved must have been owned by the departed node.
    [?assertEqual(Gone, sm_cluster:owner(Id, ?NODES)) || Id <- Moved],
    %% And nothing owned by a surviving node moved.
    Kept = [Id || Id <- ids(), sm_cluster:owner(Id, ?NODES) =/= Gone],
    [?assertEqual(sm_cluster:owner(Id, ?NODES), sm_cluster:owner(Id, Remaining)) || Id <- Kept],
    ok.

%% Load spreads across the cluster rather than piling on one node (a sanity check
%% on the hash, not a strict balance guarantee).
spread_test() ->
    Counts = lists:foldl(
        fun(Id, Acc) ->
            N = sm_cluster:owner(Id, ?NODES),
            maps:update_with(N, fun(C) -> C + 1 end, 1, Acc)
        end, #{}, ids()),
    %% Every node owns at least a few of 500 games across 4 nodes.
    [?assert(maps:get(N, Counts, 0) > 20) || N <- ?NODES],
    ok.
