# ArgoCD — GitOps for the app layer

Terraform provisions the cluster, ArgoCD deploys the apps onto it. Git is the
source of truth: `infra/helm/alekhine` on `main` is what the cluster runs, and
every change is a `git push` rather than a `helm upgrade` from a laptop.

```
 git push ──▶ GitHub ──▶ ArgoCD (in-cluster) ──▶ renders the Helm chart ──▶ cluster
                              ▲                                                │
                              └──────────── self-heal reverts drift ──────────┘
```

## Bootstrap (once per cluster)

```bash
# 1. install ArgoCD
make argocd-install

# 2. hand the release over to ArgoCD (if it was installed with `helm` first)
helm uninstall acg || true

# 3. register the app — from here on, git is in charge
kubectl apply -f infra/argocd/application.yaml

# watch it converge
kubectl -n argocd get applications alekhine -w
```

`make argocd-install` also prints the initial admin password and the
`kubectl port-forward` command for the UI (https://localhost:8080).

## How it behaves

- **Automated sync**: a push to `main` that changes the chart is rolled out
  without any manual step.
- **Self-heal**: `kubectl edit`-ing a managed object is reverted to match git —
  the cluster cannot drift from the repo.
- **Prune**: deleting a resource from the chart deletes it from the cluster.

## Note on this local setup

The single-replica, kind-specific values live in `values-kind.yaml`, which the
Application references directly. A real environment would layer an ApplicationSet
or per-environment value files (staging/prod) over the same chart — the point of
keeping deployment config in git rather than in a person's shell history.
