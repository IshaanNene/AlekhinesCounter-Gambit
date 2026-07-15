# The client is dependency-free static files, so there is no build stage:
# NGINX serves them directly and proxies the API to the gateway.
FROM nginx:1.27-alpine

COPY web/nginx.conf /etc/nginx/conf.d/default.conf
COPY web/index.html web/styles.css web/app.js web/chess.js web/graphql.js /usr/share/nginx/html/

EXPOSE 80
