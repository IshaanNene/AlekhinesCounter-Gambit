# Terraform — cluster provisioning

Terraform provisions the **cluster**; Helm deploys the **apps** onto it
(`infra/helm/alekhine`). Two lifecycles, two tools — the same split ArgoCD would
formalise, with git as the source of truth for the app layer.

Locally the target is a [kind](https://kind.sigs.k8s.io) cluster. Swapping to
EKS/GKE is a change to `main.tf` alone; the chart and the deploy flow do not move.

## Usage

```bash
cd infra/terraform
terraform init
terraform apply                 # creates the kind cluster
export KUBECONFIG="$(terraform output -raw kubeconfig_path)"

# then deploy the apps (from the repo root)
make k8s-deploy                 # builds images, loads them into kind, helm installs
make k8s-ingress                # installs the ingress-nginx controller (once)

terraform destroy               # tears the cluster down
```

The cluster publishes two host ports (see `variables.tf`): the gateway NodePort
on `gateway_host_port` (default **8088**) and the ingress-nginx controller on
`ingress_host_port` (default **8888**). After `make k8s-ingress`, the whole app
is reachable through the ingress at `terraform output -raw ingress_url`.
