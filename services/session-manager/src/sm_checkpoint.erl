%%% @doc Durable per-game session checkpoint, in Redis.
%%%
%%% The live session state a game process holds — whose turn it is, each side's
%%% remaining clock — is ephemeral: it dies with the process. That is fine for a
%%% crash (the game re-homes and rebuilds), but only if the state was written
%%% somewhere durable first. This module is that somewhere.
%%%
%%% A game checkpoints itself on every clock-affecting transition (creation, each
%%% move, and finish). When the node owning a game dies, a surviving node restores
%%% the game from its checkpoint, reconstructing the clock and deducting the wall
%%% time that passed during the outage (see sm_game). Postgres remains the source
%%% of truth for *moves*; this is only the live clock/turn overlay.
%%%
%%% Nil-safe: with Redis unconfigured (ACG_REDIS_ADDR unset) checkpointing is a
%%% no-op, so a single-node deployment still plays chess — it just cannot recover
%%% a game across a node death.
-module(sm_checkpoint).
-behaviour(gen_server).

-export([start_link/0, save/1, load/1, delete/1, enabled/0]).
-export([init/1, handle_call/3, handle_cast/2, handle_info/2, terminate/2]).

%% Key prefix for checkpoint blobs.
-define(PREFIX, "acg:session:cp:").
%% An in-progress game's checkpoint outlives any real game but is bounded so an
%% abandoned key cannot leak forever; a finished game's lingers only long enough
%% to answer a restore that races the delete, then expires.
-define(TTL_IN_PROGRESS, 21600). % 6h
-define(TTL_FINISHED, 300).      % 5m
%% Where the connected eredis client pid is published for lock-free reads.
-define(PT_CLIENT, {?MODULE, client}).

%%% API

start_link() ->
    gen_server:start_link({local, ?MODULE}, ?MODULE, [], []).

%% @doc Whether a Redis client is connected and checkpoints are being persisted.
-spec enabled() -> boolean().
enabled() ->
    is_pid(client()).

%% @doc Persist a game's checkpoint. `CP' must contain at least `game_id' and
%% `status'; the TTL is chosen from the status. Best-effort: a Redis error is
%% reported but never raised, so it cannot fail a move.
-spec save(map()) -> ok | {error, term()}.
save(#{game_id := GameId, status := Status} = CP) ->
    case client() of
        disabled ->
            ok;
        Client ->
            TTL = ttl_for(Status),
            Cmd = ["SET", key(GameId), term_to_binary(CP), "EX", integer_to_list(TTL)],
            case eredis:q(Client, Cmd) of
                {ok, _} -> ok;
                {error, Reason} -> {error, Reason}
            end
    end.

%% @doc Load a game's checkpoint, or `none' if there is not one.
-spec load(binary()) -> {ok, map()} | none | {error, term()}.
load(GameId) ->
    case client() of
        disabled ->
            none;
        Client ->
            case eredis:q(Client, ["GET", key(GameId)]) of
                {ok, undefined} -> none;
                {ok, Bin} -> {ok, binary_to_term(Bin, [safe])};
                {error, Reason} -> {error, Reason}
            end
    end.

%% @doc Delete a game's checkpoint (the game is over and will never be restored).
-spec delete(binary()) -> ok.
delete(GameId) ->
    case client() of
        disabled -> ok;
        Client ->
            _ = eredis:q(Client, ["DEL", key(GameId)]),
            ok
    end.

%%% gen_server callbacks
%%%
%%% Owns the eredis connection for its lifetime and publishes the client pid to
%%% persistent_term so save/load/delete are lock-free (they call eredis directly
%%% from the game process, which eredis pipelines). eredis links the client here,
%%% so if it drops this process dies and the supervisor restarts both, refreshing
%%% the published pid.

init([]) ->
    case redis_endpoint() of
        disabled ->
            persistent_term:put(?PT_CLIENT, disabled),
            logger:info("session checkpoint disabled (ACG_REDIS_ADDR unset)"),
            {ok, #{client => disabled}};
        {Host, Port} ->
            case eredis:start_link(Host, Port) of
                {ok, Client} ->
                    persistent_term:put(?PT_CLIENT, Client),
                    logger:info("session checkpoint connected to redis ~s:~p", [Host, Port]),
                    {ok, #{client => Client}};
                {error, Reason} ->
                    %% Degrade rather than crash-loop the whole node on a Redis
                    %% that is not up yet; play continues without cross-node
                    %% recovery until a restart reconnects.
                    persistent_term:put(?PT_CLIENT, disabled),
                    logger:warning("session checkpoint redis unavailable (~p); disabled", [Reason]),
                    {ok, #{client => disabled}}
            end
    end.

handle_call(_Request, _From, State) ->
    {reply, {error, unknown_request}, State}.

handle_cast(_Msg, State) ->
    {noreply, State}.

handle_info(_Info, State) ->
    {noreply, State}.

terminate(_Reason, _State) ->
    persistent_term:put(?PT_CLIENT, disabled),
    ok.

%%% internal

client() ->
    persistent_term:get(?PT_CLIENT, disabled).

key(GameId) when is_binary(GameId) -> <<?PREFIX, GameId/binary>>;
key(GameId) when is_list(GameId) -> ?PREFIX ++ GameId.

ttl_for(finished) -> ?TTL_FINISHED;
ttl_for(_) -> ?TTL_IN_PROGRESS.

%% ACG_REDIS_ADDR is "host:port" (matching the Go services' env). Absent or empty
%% disables checkpointing.
redis_endpoint() ->
    case os:getenv("ACG_REDIS_ADDR") of
        false -> disabled;
        "" -> disabled;
        Addr ->
            case string:split(Addr, ":", trailing) of
                [Host, PortStr] ->
                    case string:to_integer(PortStr) of
                        {Port, _} when is_integer(Port) -> {Host, Port};
                        _ -> {Host, 6379}
                    end;
                [Host] ->
                    {Host, 6379}
            end
    end.
