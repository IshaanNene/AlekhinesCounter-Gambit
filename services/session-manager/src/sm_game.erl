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
    InitialMs = maps:get(initial_ms, Opts, 300000),
    IncrementMs = maps:get(increment_ms, Opts, 0),
    GraceMs = maps:get(grace_ms, Opts, 30000),
    White = maps:get(white, Opts, undefined),
    Black = maps:get(black, Opts, undefined),
    case sm_registry:register(GameId, self()) of
        ok -> init_state(GameId, White, Black, InitialMs, IncrementMs, GraceMs);
        {error, already_registered} -> {stop, already_registered}
    end.

init_state(GameId, White, Black, InitialMs, IncrementMs, GraceMs) ->
    Clock = sm_clock:start(sm_clock:new(InitialMs, IncrementMs), white, now_ms()),
    State0 = #{
        game_id => GameId,
        white => White,
        black => Black,
        turn => white,
        clock => Clock,
        status => in_progress,
        result => undefined,
        spectators => [],
        connected => #{},
        grace_ms => GraceMs,
        grace_timers => #{},
        flag => undefined,
        epoch => 0
    },
    {ok, arm_flag_timer(State0)}.

handle_call({move, PlayerId}, _From, State = #{status := in_progress, turn := Turn}) ->
    case player_side(PlayerId, State) of
        {ok, Turn} ->
            case sm_clock:on_move(maps:get(clock, State), now_ms()) of
                {flag, Side} ->
                    Finished = finish(State, other(Side), flag),
                    {reply, {ok, snapshot(Finished)}, Finished};
                {ok, NewClock} ->
                    Moved = arm_flag_timer(State#{clock := NewClock, turn := other(Turn)}),
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

%% finish records the result and cancels all timers. Winner is a side, or `none'
%% for a draw.
finish(State, Winner, Reason) ->
    S1 = cancel_all_grace(cancel_flag(State)),
    S1#{status := finished, result := #{winner => Winner, reason => Reason}}.

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
