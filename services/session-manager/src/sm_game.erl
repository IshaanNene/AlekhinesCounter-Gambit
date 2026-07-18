%%% @doc One gen_server per live game. Owns the *session* state — whose turn it
%%% is, both chess clocks, who is connected, and spectators — while move legality
%%% and durable history live in the game-service. Responsibilities:
%%%
%%%   * Track turn and apply Fischer-increment clocks on each move.
%%%   * Fire flag-fall when a side's clock runs out (timer-driven, even if idle).
%%%   * Hold a grace window on disconnect; adjudicate abandonment if it expires.
%%%   * Handle resignation.
%%%
%%% The process registers itself in {@link sm_registry} under its game id.
-module(sm_game).
-behaviour(gen_server).

-export([start_link/2]).
-export([init/1, handle_call/3, handle_cast/2, handle_info/2, terminate/2]).

-type side() :: white | black.

start_link(GameId, Opts) ->
    gen_server:start_link(?MODULE, [GameId, Opts], []).

%%% gen_server callbacks

init([GameId, Opts]) ->
    %% Registering under the game id is also the guard against a duplicate: if the
    %% id is already live (here or on another node) syn refuses and we stop.
    case sm_registry:register(GameId, self()) of
        ok ->
            case maps:get(restore, Opts, undefined) of
                undefined -> init_fresh(GameId, Opts);
                CP -> init_restore(GameId, CP)
            end;
        {error, already_registered} ->
            {stop, already_registered}
    end.

%% A brand-new game: full clocks, White to move.
init_fresh(GameId, Opts) ->
    InitialMs = maps:get(initial_ms, Opts, 300000),
    IncrementMs = maps:get(increment_ms, Opts, 0),
    GraceMs = maps:get(grace_ms, Opts, 30000),
    White = maps:get(white, Opts, undefined),
    Black = maps:get(black, Opts, undefined),
    Clock = sm_clock:start(sm_clock:new(InitialMs, IncrementMs), white, now_ms()),
    State0 = base_state(GameId, White, Black, white, Clock, in_progress, undefined, GraceMs),
    State = arm_flag_timer(State0),
    checkpoint(State),
    {ok, State}.

%% A game re-homed onto this node after its previous owner died. The durable
%% checkpoint carries each side's clock as of the last move; the side that was on
%% the move kept "thinking" through the outage, so we deduct the wall time that
%% has passed since the checkpoint from its clock — and if that ran it out, the
%% game was in fact lost on time during the outage, so we settle it now.
init_restore(GameId, CP) ->
    #{
        white := White, black := Black, increment_ms := Inc, grace_ms := Grace,
        white_ms := WMs, black_ms := BMs, turn := Turn, status := Status,
        result := Result, checkpoint_at := CkptAt
    } = CP,
    case Status of
        finished ->
            Clock = sm_clock:restore(WMs, BMs, Inc, none, now_ms()),
            {ok, base_state(GameId, White, Black, Turn, Clock, finished, Result, Grace)};
        in_progress ->
            Elapsed = max(0, erlang:system_time(millisecond) - CkptAt),
            Running = Turn, % the side to move is the side whose clock runs
            {AdjW, AdjB} = deduct(Running, Elapsed, WMs, BMs),
            Clock = sm_clock:restore(AdjW, AdjB, Inc, Running, now_ms()),
            State0 = base_state(GameId, White, Black, Turn, Clock, in_progress, undefined, Grace),
            case running_ms(Running, AdjW, AdjB) =< 0 of
                true ->
                    %% Flag fell during the outage; the mover loses on time.
                    {ok, finish(State0, other(Running), flag)};
                false ->
                    State = arm_flag_timer(State0),
                    checkpoint(State),
                    {ok, State}
            end
    end.

base_state(GameId, White, Black, Turn, Clock, Status, Result, GraceMs) ->
    #{
        game_id => GameId,
        white => White,
        black => Black,
        turn => Turn,
        clock => Clock,
        status => Status,
        result => Result,
        spectators => [],
        connected => #{},
        grace_ms => GraceMs,
        grace_timers => #{},
        flag => undefined,
        epoch => 0
    }.

deduct(white, E, W, B) -> {max(0, W - E), B};
deduct(black, E, W, B) -> {W, max(0, B - E)}.

running_ms(white, W, _B) -> W;
running_ms(black, _W, B) -> B.

handle_call({move, PlayerId}, _From, State = #{status := in_progress, turn := Turn}) ->
    case player_side(PlayerId, State) of
        {ok, Turn} ->
            case sm_clock:on_move(maps:get(clock, State), now_ms()) of
                {flag, Side} ->
                    Finished = finish(State, other(Side), flag),
                    {reply, {ok, snapshot(Finished)}, Finished};
                {ok, NewClock} ->
                    Moved = arm_flag_timer(State#{clock := NewClock, turn := other(Turn)}),
                    %% Persist the new clock/turn before acking, so a node death
                    %% right after this move cannot lose it — the game re-homes
                    %% from exactly this checkpoint.
                    checkpoint(Moved),
                    {reply, {ok, snapshot(Moved)}, Moved}
            end;
        {ok, _OtherSide} ->
            {reply, {error, not_your_turn}, State};
        error ->
            {reply, {error, not_a_player}, State}
    end;
handle_call({move, _PlayerId}, _From, State) ->
    {reply, {error, game_over}, State};
handle_call({resign, PlayerId}, _From, State = #{status := in_progress}) ->
    case player_side(PlayerId, State) of
        {ok, Side} ->
            Finished = finish(State, other(Side), resignation),
            {reply, {ok, snapshot(Finished)}, Finished};
        error ->
            {reply, {error, not_a_player}, State}
    end;
handle_call({resign, _PlayerId}, _From, State) ->
    {reply, {error, game_over}, State};
%% The game-service reports a chess-level termination (checkmate, stalemate,
%% draw) that this process cannot observe on its own.
handle_call({end_game, Winner, Reason}, _From, State = #{status := in_progress}) ->
    Finished = finish(State, Winner, Reason),
    {reply, {ok, snapshot(Finished)}, Finished};
handle_call({end_game, _Winner, _Reason}, _From, State) ->
    %% Already finished (e.g. a flag beat the report) — report current state.
    {reply, {ok, snapshot(State)}, State};
handle_call({join, PlayerId}, _From, State) ->
    case player_side(PlayerId, State) of
        {ok, _Side} ->
            {reply, ok, mark_connected(PlayerId, true, cancel_grace(PlayerId, State))};
        error ->
            {reply, {error, not_a_player}, State}
    end;
handle_call({disconnect, PlayerId}, _From, State = #{status := in_progress}) ->
    case player_side(PlayerId, State) of
        {ok, _Side} ->
            S1 = mark_connected(PlayerId, false, cancel_grace(PlayerId, State)),
            GraceMs = maps:get(grace_ms, S1),
            Ref = erlang:send_after(GraceMs, self(), {grace_expired, PlayerId}),
            GT = maps:put(PlayerId, Ref, maps:get(grace_timers, S1)),
            {reply, ok, S1#{grace_timers := GT}};
        error ->
            {reply, {error, not_a_player}, State}
    end;
handle_call({disconnect, _PlayerId}, _From, State) ->
    {reply, {error, game_over}, State};
handle_call({reconnect, PlayerId}, _From, State) ->
    case player_side(PlayerId, State) of
        {ok, _Side} ->
            {reply, ok, mark_connected(PlayerId, true, cancel_grace(PlayerId, State))};
        error ->
            {reply, {error, not_a_player}, State}
    end;
handle_call({watch, SpectatorId}, _From, State = #{spectators := Sp}) ->
    {reply, ok, State#{spectators := lists:usort([SpectatorId | Sp])}};
handle_call({unwatch, SpectatorId}, _From, State = #{spectators := Sp}) ->
    {reply, ok, State#{spectators := lists:delete(SpectatorId, Sp)}};
handle_call(snapshot, _From, State) ->
    {reply, snapshot(State), State};
handle_call(_Request, _From, State) ->
    {reply, {error, unknown_request}, State}.

handle_cast(_Msg, State) ->
    {noreply, State}.

%% Flag-fall: the running side's clock expired while idle.
handle_info({flag_fired, Side, Epoch}, State = #{status := in_progress, flag := {_Ref, Side, Epoch}}) ->
    {noreply, finish(State#{flag := undefined}, other(Side), flag)};
handle_info({flag_fired, _Side, _Epoch}, State) ->
    %% Stale timer (turn changed or game ended) — ignore.
    {noreply, State};
%% Grace window elapsed while a player was still disconnected: they abandon.
handle_info({grace_expired, PlayerId}, State = #{status := in_progress}) ->
    case maps:get(PlayerId, maps:get(connected, State), true) of
        false ->
            {ok, Side} = player_side(PlayerId, State),
            {noreply, finish(State, other(Side), abandonment)};
        true ->
            {noreply, State}
    end;
handle_info({grace_expired, _PlayerId}, State) ->
    {noreply, State};
handle_info(_Info, State) ->
    {noreply, State}.

terminate(_Reason, _State) ->
    ok.

%%% internal helpers

%% arm_flag_timer cancels any pending flag timer and sets a new one for the side
%% whose clock is now running, tagged with a fresh epoch so stale timers are
%% ignored.
arm_flag_timer(State = #{clock := Clock, epoch := Epoch}) ->
    S1 = cancel_flag(State),
    case sm_clock:running(Clock) of
        none ->
            S1;
        Side ->
            Left = max(0, sm_clock:time_left(Clock, Side, now_ms())),
            NextEpoch = Epoch + 1,
            Ref = erlang:send_after(Left, self(), {flag_fired, Side, NextEpoch}),
            S1#{flag := {Ref, Side, NextEpoch}, epoch := NextEpoch}
    end.

cancel_flag(State = #{flag := undefined}) ->
    State;
cancel_flag(State = #{flag := {Ref, _Side, _Epoch}}) ->
    _ = erlang:cancel_timer(Ref),
    State#{flag := undefined}.

cancel_grace(PlayerId, State = #{grace_timers := GT}) ->
    case maps:take(PlayerId, GT) of
        {Ref, GT2} ->
            _ = erlang:cancel_timer(Ref),
            State#{grace_timers := GT2};
        error ->
            State
    end.

cancel_all_grace(State = #{grace_timers := GT}) ->
    maps:foreach(fun(_P, Ref) -> erlang:cancel_timer(Ref) end, GT),
    State#{grace_timers := #{}}.

mark_connected(PlayerId, Bool, State = #{connected := Conn}) ->
    State#{connected := maps:put(PlayerId, Bool, Conn)}.

%% finish records the result, cancels all timers, and checkpoints the settled
%% game so a restore that races the finish still sees the correct outcome (the
%% finished checkpoint carries a short TTL and then expires). Winner is a side,
%% or `none' for a draw.
finish(State, Winner, Reason) ->
    S1 = cancel_all_grace(cancel_flag(State)),
    S2 = S1#{status := finished, result := #{winner => Winner, reason => Reason}},
    checkpoint(S2),
    S2.

%% checkpoint writes the live session state to durable storage. Times are the
%% current values (time_left accounts for the running clock); the wall-clock
%% stamp lets a restoring node deduct the outage. Best-effort and nil-safe.
checkpoint(State = #{game_id := G, white := W, black := B, turn := T,
                     clock := C, status := S, result := R, grace_ms := Grace}) ->
    Now = now_ms(),
    _ = sm_checkpoint:save(#{
        game_id => G,
        white => W,
        black => B,
        increment_ms => sm_clock:increment(C),
        grace_ms => Grace,
        white_ms => max(0, sm_clock:time_left(C, white, Now)),
        black_ms => max(0, sm_clock:time_left(C, black, Now)),
        turn => T,
        status => S,
        result => R,
        checkpoint_at => erlang:system_time(millisecond)
    }),
    State.

-spec player_side(term(), map()) -> {ok, side()} | error.
player_side(PlayerId, _State) when PlayerId =:= undefined ->
    error;
player_side(PlayerId, #{white := PlayerId}) ->
    {ok, white};
player_side(PlayerId, #{black := PlayerId}) ->
    {ok, black};
player_side(_PlayerId, _State) ->
    error.

snapshot(State) ->
    #{
        game_id := G, white := W, black := B, turn := T, clock := C,
        status := S, result := R, spectators := Sp, connected := Conn
    } = State,
    Now = now_ms(),
    #{
        game_id => G,
        white => W,
        black => B,
        turn => T,
        status => S,
        result => R,
        spectators => Sp,
        connected => Conn,
        time_left => #{
            white => max(0, sm_clock:time_left(C, white, Now)),
            black => max(0, sm_clock:time_left(C, black, Now))
        }
    }.

other(white) -> black;
other(black) -> white.

now_ms() ->
    erlang:monotonic_time(millisecond).
