# The client is dependency-free static files, so there is no bundler. It does
# need one build step: stamping a version onto every module URL.
#
# Without it the filenames are constant (app.js, never app.a1b2c3.js), so a
# browser has no way to know the contents changed. Cache headers alone do not
# save you — an ES module already in a page's module map is reused, and in
# practice this served a stale app.js against a newer GraphQL schema. Changing
# the URL is the only thing a cache cannot argue with.
FROM alpine:3.20 AS stamp
WORKDIR /src
COPY web/ ./
# Distinct per build; a content hash would be better, but that needs a bundler.
ARG BUILD_ID=dev
RUN BUILD="${BUILD_ID}-$(date +%s)" && \
    # Both the entry point in the HTML and every relative import inside it: the
    # imports resolve without the query otherwise, and stay cached.
    sed -i "s|src=\"\./app\.js\"|src=\"./app.js?v=${BUILD}\"|" index.html && \
    sed -i -E "s|from \"\./([a-z-]+)\.js\"|from \"./\1.js?v=${BUILD}\"|g" ./*.js && \
    sed -i -E "s|import\(\"\./([a-z-]+)\.js\"\)|import(\"./\1.js?v=${BUILD}\")|g" ./*.js && \
    echo "stamped build ${BUILD}"

FROM nginx:1.27-alpine
COPY web/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=stamp /src/index.html /src/styles.css /src/app.js /src/chess.js \
     /src/graphql.js /src/settings.js /src/settings-ui.js /src/account.js \
     /src/analysis.js /usr/share/nginx/html/

EXPOSE 80
