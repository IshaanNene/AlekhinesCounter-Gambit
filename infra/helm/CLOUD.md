# Deploying to a real cloud cluster

The local flow (`make k8s-deploy`) runs everything — including Postgres, Kafka,
and object storage — inside a kind cluster. This runbook deploys the same Helm
chart to a real, managed Kubernetes cluster (EKS, GKE, AKS) using
[`values-cloud.yaml`](alekhine/values-cloud.yaml), which:

- pulls the images CI publishes to GHCR on every merge to `main`;
- moves the **durable** stores to managed services — **Postgres** (RDS / Cloud
  SQL) and **object storage** (S3 / GCS);
- keeps **redis-stack, Kafka, and Jaeger in-cluster**. Redis stays in-cluster
  deliberately: the analysis pipeline uses **RedisBloom + RedisTimeSeries**
  (novelty + fair-play), which ElastiCache/Memorystore do not provide. Kafka is
  disposable (Postgres is the source of truth), so it is cheaper here than MSK.

Cloud-agnostic by design — the cluster itself is bring-your-own for now (an EKS
or GKE Terraform module is the next step; the chart and this runbook do not
change when it lands).

## Prerequisites

- A Kubernetes cluster and a `kubectl` context pointing at it.
- An **ingress controller** (ingress-nginx) and, for TLS, **cert-manager** with a
  ClusterIssuer.
- A **managed Postgres** instance, reachable from the cluster, with an `acg`
  database.
- A **bucket** for PGN/analysis archives and the opening book (S3 or GCS with S3
  interoperability), plus an access key / secret.
- The GHCR images accessible to the cluster: either make the `acg-*` packages
  public, or create an `imagePullSecret` and reference it.

## Steps

**1. Managed Postgres + bucket.** Create them in your cloud (RDS + S3, or Cloud
SQL + GCS). Note the Postgres DSN and the bucket's endpoint + credentials.
`ACG_RUN_MIGRATIONS=true` (the default) applies the schema on first boot.

**2. Point the object-storage env at your bucket.** Edit the `ACG_S3_ENDPOINT`
/ `ACG_S3_PUBLIC_ENDPOINT` values in `values-cloud.yaml` (defaults assume AWS
S3; for GCS use `storage.googleapis.com`).

**3. Install, passing secrets out-of-band** (never commit them):

```bash
helm upgrade --install acg infra/helm/alekhine \
  -f infra/helm/alekhine/values-cloud.yaml \
  --set ingress.host="chess.yourdomain.com" \
  --set secrets.postgresDSN="postgres://acg:PASS@your-postgres:5432/acg?sslmode=require" \
  --set secrets.sessionSecret="$(openssl rand -base64 32)" \
  --set secrets.erlangCookie="$(openssl rand -hex 24)" \
  --set secrets.s3AccessKey="AKIA…" \
  --set secrets.s3SecretKey="…" \
  --wait --timeout 10m
```

For anything beyond a demo, use a real secret store (AWS Secrets Manager / GCP
Secret Manager via the external-secrets operator, or sealed-secrets) instead of
`--set`.

**4. DNS + TLS.** Point your host at the ingress-nginx LoadBalancer's address,
and add a cert-manager annotation/Ingress TLS block for the host. `ACG_COOKIE_SECURE`
is already `true` in the cloud values.

**5. Verify.**

```bash
kubectl get pods                       # all Running; session-manager has 3
kubectl get svc session-manager-headless
curl -sf https://chess.yourdomain.com/healthz
# play a game in the browser, then watch it live: /watch.html?game=<id>
```

**6. Prove the scale claims on real infra.** This is the payoff — turn the local
"floor" numbers into cluster numbers:

```bash
# spectator fanout under load (one hot game)
k6 run -e GAME=<id> -e WS=wss://chess.yourdomain.com -e VUS=5000 load/k6/fanout.js

# no-SPOF: kill a session-manager node, live games re-home
NS=default TARGET=https://chess.yourdomain.com/graphql \
  load/chaos/chaos.sh session-handoff
```

## Notes

- **Erlang clustering ports.** The session-manager nodes cluster over epmd
  (`4369`) and the pinned distribution port (`9100`); the headless Service handles
  peer discovery by DNS. Most cloud CNIs allow pod-to-pod traffic by default, so
  this just works — but if you enforce NetworkPolicies, allow those ports between
  session-manager pods.
- **EKS / GKE swap.** When the Terraform module lands, only `infra/terraform`
  changes; this chart, `values-cloud.yaml`, and the steps above are unchanged —
  that separation (Terraform owns the cluster, Helm owns the apps) is the whole
  point.
- **Kafka via MSK.** To use MSK instead of the in-cluster broker, set
  `infraServices.kafka.enabled=false` and point `ACG_KAFKA_BROKERS` at the MSK
  bootstrap servers.
