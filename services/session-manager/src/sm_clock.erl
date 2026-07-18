%%% @doc Chess clock logic as pure functions (no processes, fully testable).
%%%
%%% A clock tracks each side's remaining milliseconds, a Fischer increment, which
%%% side's clock is currently running, and the timestamp at which the running
%%% side's turn began. Callers pass an explicit `Now' (monotonic milliseconds) so
%%% behaviour is deterministic and testable.
-module(sm_clock).

-export([new/2, start/3, on_move/2, time_left/3, running/1, remaining/1]).
-export([increment/1, restore/5]).

-export_type([clock/0, side/0]).

-type side() :: white | black.
-type clock() :: #{
    white := non_neg_integer(),
    black := non_neg_integer(),
    increment := non_neg_integer(),
    running := side() | none,
    since := integer() | undefined
}.

%% @doc A fresh clock with `InitialMs' on both sides and an `IncrementMs' bonus
%% added after each completed move. Not yet running.
-spec new(non_neg_integer(), non_neg_integer()) -> clock().
new(InitialMs, IncrementMs) ->
    #{
        white => InitialMs,
        black => InitialMs,
        increment => IncrementMs,
        running => none,
        since => undefined
    }.

%% @doc Start `Side''s clock running as of `Now'.
-spec start(clock(), side(), integer()) -> clock().
start(Clock, Side, Now) ->
    Clock#{running := Side, since := Now}.

%% @doc Register that the side whose clock is running has completed a move at
%% `Now'. Deducts elapsed time; if that exhausts the clock the mover has flagged
%% and loses. Otherwise the increment is added and the opponent's clock starts.
-spec on_move(clock(), integer()) ->
    {ok, clock()} | {flag, side()}.
on_move(#{running := none} = Clock, _Now) ->
    {ok, Clock};
on_move(#{running := Side, since := Since, increment := Inc} = Clock, Now) ->
    Left = maps:get(Side, Clock) - (Now - Since),
    case Left =< 0 of
        true ->
            {flag, Side};
        false ->
            Other = other(Side),
            {ok, Clock#{Side := Left + Inc, running := Other, since := Now}}
    end.

%% @doc Milliseconds left for `Side' as of `Now', accounting for time already
%% burned if `Side''s clock is currently running.
-spec time_left(clock(), side(), integer()) -> integer().
time_left(#{running := Side, since := Since} = Clock, Side, Now) ->
    maps:get(Side, Clock) - (Now - Since);
time_left(Clock, Side, _Now) ->
    maps:get(Side, Clock).

%% @doc Which side's clock is running (or `none').
-spec running(clock()) -> side() | none.
running(#{running := R}) -> R.

%% @doc The stored remaining times as `{White, Black}' (not adjusted for a
%% running clock; use time_left/3 for a live value).
-spec remaining(clock()) -> {non_neg_integer(), non_neg_integer()}.
remaining(#{white := W, black := B}) -> {W, B}.

%% @doc The Fischer increment added after each move.
-spec increment(clock()) -> non_neg_integer().
increment(#{increment := Inc}) -> Inc.

%% @doc Rebuild a clock with explicit per-side remaining times, for restoring a
%% game onto a new node after a handoff. `Running' is the side whose clock is
%% ticking as of `Now' (or `none' for a settled clock). The caller is expected to
%% have already deducted any time that elapsed during the outage from the running
%% side, so the reconstructed clock runs forward from `Now'.
-spec restore(non_neg_integer(), non_neg_integer(), non_neg_integer(), side() | none, integer()) -> clock().
restore(WhiteMs, BlackMs, Inc, none, _Now) ->
    #{white => WhiteMs, black => BlackMs, increment => Inc, running => none, since => undefined};
restore(WhiteMs, BlackMs, Inc, Running, Now) ->
    #{white => WhiteMs, black => BlackMs, increment => Inc, running => Running, since => Now}.

%%% internal

other(white) -> black;
other(black) -> white.
