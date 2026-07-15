%%%-------------------------------------------------------------------
%% @doc Behaviour to implement for grpc service alekhine.session.v1.SessionService.
%% @end
%%%-------------------------------------------------------------------

%% this module was generated and should not be modified manually

-module(alekhine_session_v_1_session_service_bhvr).

%% Unary RPC
-callback create_session(ctx:t(), session_pb:create_session_request()) ->
    {ok, session_pb:snapshot(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback join(ctx:t(), session_pb:player_ref()) ->
    {ok, session_pb:snapshot(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback move_made(ctx:t(), session_pb:player_ref()) ->
    {ok, session_pb:snapshot(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback disconnect(ctx:t(), session_pb:player_ref()) ->
    {ok, session_pb:snapshot(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback reconnect(ctx:t(), session_pb:player_ref()) ->
    {ok, session_pb:snapshot(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback resign(ctx:t(), session_pb:player_ref()) ->
    {ok, session_pb:snapshot(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback end_session(ctx:t(), session_pb:end_session_request()) ->
    {ok, session_pb:snapshot(), ctx:t()} | grpcbox_stream:grpc_error_response().

%% Unary RPC
-callback get_snapshot(ctx:t(), session_pb:game_ref()) ->
    {ok, session_pb:snapshot(), ctx:t()} | grpcbox_stream:grpc_error_response().

