%%% @doc Application entry point for the session manager. Starts the supervision
%%% tree and, unless disabled, the gRPC server exposing
%%% `alekhine.session.v1.SessionService' to the Go services.
-module(session_manager_app).
-behaviour(application).

-export([start/2, stop/1]).

-define(SERVICE_NAME, 'alekhine.session.v1.SessionService').
-define(DEFAULT_PORT, 50053).

start(_Type, _Args) ->
    case session_manager_sup:start_link() of
        {ok, Pid} ->
            case maybe_start_grpc() of
                ok -> {ok, Pid};
                {error, Reason} -> {error, Reason}
            end;
        Error ->
            Error
    end.

stop(_State) ->
    ok.

%%% internal

maybe_start_grpc() ->
    case application:get_env(session_manager, start_grpc, true) of
        false ->
            ok;
        true ->
            Port = grpc_port(),
            Opts = #{
                grpc_opts => #{
                    service_protos => [session_pb],
                    services => #{?SERVICE_NAME => sm_grpc_service}
                },
                listen_opts => #{port => Port, ip => {0, 0, 0, 0}}
            },
            case grpcbox:start_server(Opts) of
                {ok, _ServerPid} ->
                    logger:info("session-manager gRPC listening on port ~p", [Port]),
                    ok;
                {error, Reason} ->
                    logger:error("session-manager gRPC failed to start on port ~p: ~p", [Port, Reason]),
                    {error, {grpc_start_failed, Reason}}
            end
    end.

%% Port precedence: ACG_SESSION_PORT env var, then app env, then the default.
grpc_port() ->
    case os:getenv("ACG_SESSION_PORT") of
        false -> application:get_env(session_manager, grpc_port, ?DEFAULT_PORT);
        Str -> list_to_integer(Str)
    end.
