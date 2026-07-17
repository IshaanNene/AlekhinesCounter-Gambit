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

terraform destroy               # tears the cluster down
```
