%%% @doc Top-level supervisor. Supervises the game registry and the dynamic
%%% supervisor that owns one process per live game. A one_for_one strategy keeps
%%% the registry and the game supervisor independent: if one restarts, the other
%%% is untouched.
-module(session_manager_sup).
-behaviour(supervisor).

-export([start_link/0, init/1]).

start_link() ->
    supervisor:start_link({local, ?MODULE}, ?MODULE, []).

init([]) ->
    SupFlags = #{strategy => one_for_one, intensity => 10, period => 10},
    %% Registry (adds this node to the syn scope) and checkpoint (connects Redis)
    %% must be up before any game starts, since a game registers and checkpoints
    %% itself in init. sm_cluster keeps the node connected to its peers.
    Registry = #{
        id => sm_registry,
        start => {sm_registry, start_link, []},
        restart => permanent,
        type => worker
    },
    Cluster = #{
        id => sm_cluster,
        start => {sm_cluster, start_link, []},
        restart => permanent,
        type => worker
    },
    Checkpoint = #{
        id => sm_checkpoint,
        start => {sm_checkpoint, start_link, []},
        restart => permanent,
        type => worker
    },
    GameSup = #{
        id => sm_game_sup,
        start => {sm_game_sup, start_link, []},
        restart => permanent,
        type => supervisor
    },
    {ok, {SupFlags, [Registry, Cluster, Checkpoint, GameSup]}}.
