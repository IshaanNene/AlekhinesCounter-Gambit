# Terraform — AWS / EKS

Provisions the AWS substrate the platform runs on, as a self-contained
alternative to the local kind setup in `../` (Terraform owns the cluster + managed
stores; Helm owns the apps). It creates:

- a **VPC** (public + private subnets across N AZs, one NAT gateway);
- an **EKS** cluster + a managed node group;
- **RDS PostgreSQL** in private subnets, reachable only from the nodes;
- three **S3 buckets** (`<prefix>pgn/analysis/books`) + a least-privilege IAM user;
- outputs that assemble the `helm install` for you.

> **Cost warning.** EKS control plane, a NAT gateway, RDS, and EC2 nodes all bill
> by the hour. Run `terraform destroy` when you are done.

## Usage

```bash
cd infra/terraform/eks
terraform init
terraform apply                          # ~15–20 min (EKS + RDS take a while)

# point kubectl at the new cluster
$(terraform output -raw configure_kubectl)

# install an ingress controller (once); its LB address is your app's front door
helm --namespace ingress-nginx --create-namespace install ingress-nginx \
  ingress-nginx --repo https://kubernetes.github.io/ingress-nginx

# deploy the apps — the command is generated with all secrets wired in
terraform output -raw helm_install_command | bash

# then point your DNS at the ingress-nginx LoadBalancer and add TLS (cert-manager)
kubectl get svc -n ingress-nginx
```

See [../../helm/CLOUD.md](../../helm/CLOUD.md) for the full deploy runbook
(DNS/TLS, verification, and running the fanout k6 + `session-handoff` chaos on the
real cluster).

## Notes

- **Secrets never touch git.** The DSN, S3 keys, and bucket prefix are read from
  Terraform outputs into `helm install` at apply time (`terraform output -raw …`).
- **Static S3 keys vs. IRSA.** The app reads `ACG_S3_ACCESS_KEY/SECRET`, so this
  provisions an IAM user. IRSA (enabled on the cluster) is the keyless upgrade:
  attach a role to the app's ServiceAccount and drop the user.
- **Cost knobs.** `single_nat_gateway`, `db.t3.small`, `db_multi_az=false`, and a
  small node group keep a demo cheap; the variables expose all of them for a real
  environment.
- **GKE.** The same chart + `values-cloud.yaml` also deploy to GCP — see the
  parallel [`../gke`](../gke) module (GKE + Cloud SQL + GCS).
