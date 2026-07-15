%%%-------------------------------------------------------------------
%% @doc Client module for grpc service alekhine.session.v1.SessionService.
%% @end
%%%-------------------------------------------------------------------

%% this module was generated and should not be modified manually

-module(alekhine_session_v_1_session_service_client).

-compile(export_all).
-compile(nowarn_export_all).

-include_lib("grpcbox/include/grpcbox.hrl").

-define(is_ctx(Ctx), is_tuple(Ctx) andalso element(1, Ctx) =:= ctx).

-define(SERVICE, 'alekhine.session.v1.SessionService').
-define(PROTO_MODULE, 'session_pb').
-define(MARSHAL_FUN(T), fun(I) -> ?PROTO_MODULE:encode_msg(I, T) end).
-define(UNMARSHAL_FUN(T), fun(I) -> ?PROTO_MODULE:decode_msg(I, T) end).
-define(DEF(Input, Output, MessageType), #grpcbox_def{service=?SERVICE,
                                                      message_type=MessageType,
                                                      marshal_fun=?MARSHAL_FUN(Input),
                                                      unmarshal_fun=?UNMARSHAL_FUN(Output)}).

%% @doc Unary RPC
-spec create_session(session_pb:create_session_request()) ->
    {ok, session_pb:snapshot(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
create_session(Input) ->
    create_session(ctx:new(), Input, #{}).

-spec create_session(ctx:t() | session_pb:create_session_request(), session_pb:create_session_request() | grpcbox_client:options()) ->
    {ok, session_pb:snapshot(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
create_session(Ctx, Input) when ?is_ctx(Ctx) ->
    create_session(Ctx, Input, #{});
create_session(Input, Options) ->
    create_session(ctx:new(), Input, Options).

-spec create_session(ctx:t(), session_pb:create_session_request(), grpcbox_client:options()) ->
    {ok, session_pb:snapshot(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
create_session(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/alekhine.session.v1.SessionService/CreateSession">>, Input, ?DEF(create_session_request, snapshot, <<"alekhine.session.v1.CreateSessionRequest">>), Options).

%% @doc Unary RPC
-spec join(session_pb:player_ref()) ->
    {ok, session_pb:snapshot(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
join(Input) ->
    join(ctx:new(), Input, #{}).

-spec join(ctx:t() | session_pb:player_ref(), session_pb:player_ref() | grpcbox_client:options()) ->
    {ok, session_pb:snapshot(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
join(Ctx, Input) when ?is_ctx(Ctx) ->
    join(Ctx, Input, #{});
join(Input, Options) ->
    join(ctx:new(), Input, Options).

-spec join(ctx:t(), session_pb:player_ref(), grpcbox_client:options()) ->
    {ok, session_pb:snapshot(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
join(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/alekhine.session.v1.SessionService/Join">>, Input, ?DEF(player_ref, snapshot, <<"alekhine.session.v1.PlayerRef">>), Options).

%% @doc Unary RPC
-spec move_made(session_pb:player_ref()) ->
    {ok, session_pb:snapshot(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
move_made(Input) ->
    move_made(ctx:new(), Input, #{}).

-spec move_made(ctx:t() | session_pb:player_ref(), session_pb:player_ref() | grpcbox_client:options()) ->
    {ok, session_pb:snapshot(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
move_made(Ctx, Input) when ?is_ctx(Ctx) ->
    move_made(Ctx, Input, #{});
move_made(Input, Options) ->
    move_made(ctx:new(), Input, Options).

-spec move_made(ctx:t(), session_pb:player_ref(), grpcbox_client:options()) ->
    {ok, session_pb:snapshot(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
move_made(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/alekhine.session.v1.SessionService/MoveMade">>, Input, ?DEF(player_ref, snapshot, <<"alekhine.session.v1.PlayerRef">>), Options).

%% @doc Unary RPC
-spec disconnect(session_pb:player_ref()) ->
    {ok, session_pb:snapshot(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
disconnect(Input) ->
    disconnect(ctx:new(), Input, #{}).

-spec disconnect(ctx:t() | session_pb:player_ref(), session_pb:player_ref() | grpcbox_client:options()) ->
    {ok, session_pb:snapshot(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
disconnect(Ctx, Input) when ?is_ctx(Ctx) ->
    disconnect(Ctx, Input, #{});
disconnect(Input, Options) ->
    disconnect(ctx:new(), Input, Options).

-spec disconnect(ctx:t(), session_pb:player_ref(), grpcbox_client:options()) ->
    {ok, session_pb:snapshot(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
disconnect(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/alekhine.session.v1.SessionService/Disconnect">>, Input, ?DEF(player_ref, snapshot, <<"alekhine.session.v1.PlayerRef">>), Options).

%% @doc Unary RPC
-spec reconnect(session_pb:player_ref()) ->
    {ok, session_pb:snapshot(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
reconnect(Input) ->
    reconnect(ctx:new(), Input, #{}).

-spec reconnect(ctx:t() | session_pb:player_ref(), session_pb:player_ref() | grpcbox_client:options()) ->
    {ok, session_pb:snapshot(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
reconnect(Ctx, Input) when ?is_ctx(Ctx) ->
    reconnect(Ctx, Input, #{});
reconnect(Input, Options) ->
    reconnect(ctx:new(), Input, Options).

-spec reconnect(ctx:t(), session_pb:player_ref(), grpcbox_client:options()) ->
    {ok, session_pb:snapshot(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
reconnect(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/alekhine.session.v1.SessionService/Reconnect">>, Input, ?DEF(player_ref, snapshot, <<"alekhine.session.v1.PlayerRef">>), Options).

%% @doc Unary RPC
-spec resign(session_pb:player_ref()) ->
    {ok, session_pb:snapshot(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
resign(Input) ->
    resign(ctx:new(), Input, #{}).

-spec resign(ctx:t() | session_pb:player_ref(), session_pb:player_ref() | grpcbox_client:options()) ->
    {ok, session_pb:snapshot(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
resign(Ctx, Input) when ?is_ctx(Ctx) ->
    resign(Ctx, Input, #{});
resign(Input, Options) ->
    resign(ctx:new(), Input, Options).

-spec resign(ctx:t(), session_pb:player_ref(), grpcbox_client:options()) ->
    {ok, session_pb:snapshot(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
resign(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/alekhine.session.v1.SessionService/Resign">>, Input, ?DEF(player_ref, snapshot, <<"alekhine.session.v1.PlayerRef">>), Options).

%% @doc Unary RPC
-spec end_session(session_pb:end_session_request()) ->
    {ok, session_pb:snapshot(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
end_session(Input) ->
    end_session(ctx:new(), Input, #{}).

-spec end_session(ctx:t() | session_pb:end_session_request(), session_pb:end_session_request() | grpcbox_client:options()) ->
    {ok, session_pb:snapshot(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
end_session(Ctx, Input) when ?is_ctx(Ctx) ->
    end_session(Ctx, Input, #{});
end_session(Input, Options) ->
    end_session(ctx:new(), Input, Options).

-spec end_session(ctx:t(), session_pb:end_session_request(), grpcbox_client:options()) ->
    {ok, session_pb:snapshot(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
end_session(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/alekhine.session.v1.SessionService/EndSession">>, Input, ?DEF(end_session_request, snapshot, <<"alekhine.session.v1.EndSessionRequest">>), Options).

%% @doc Unary RPC
-spec get_snapshot(session_pb:game_ref()) ->
    {ok, session_pb:snapshot(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
get_snapshot(Input) ->
    get_snapshot(ctx:new(), Input, #{}).

-spec get_snapshot(ctx:t() | session_pb:game_ref(), session_pb:game_ref() | grpcbox_client:options()) ->
    {ok, session_pb:snapshot(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
get_snapshot(Ctx, Input) when ?is_ctx(Ctx) ->
    get_snapshot(Ctx, Input, #{});
get_snapshot(Input, Options) ->
    get_snapshot(ctx:new(), Input, Options).

-spec get_snapshot(ctx:t(), session_pb:game_ref(), grpcbox_client:options()) ->
    {ok, session_pb:snapshot(), grpcbox:metadata()} | grpcbox_stream:grpc_error_response() | {error, any()}.
get_snapshot(Ctx, Input, Options) ->
    grpcbox_client:unary(Ctx, <<"/alekhine.session.v1.SessionService/GetSnapshot">>, Input, ?DEF(game_ref, snapshot, <<"alekhine.session.v1.GameRef">>), Options).

