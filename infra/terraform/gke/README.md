# Terraform — GCP / GKE

The GCP analog of `../eks`: provisions the whole substrate the cloud Helm values
expect, so the same chart runs on GKE. It creates:

- a **VPC-native** network (subnet + secondary ranges for pods/services);
- a **GKE** cluster + an autoscaling node pool;
- **Cloud SQL PostgreSQL** on a **private IP** (VPC peering), reachable only from
  the cluster;
- three **GCS buckets** (`<prefix>pgn/analysis/books`) + a service account whose
  **HMAC key** the app uses over GCS's S3-compatible endpoint;
- outputs that assemble the `helm install` for you.

> **Cost warning.** GKE, Cloud SQL, and nodes bill by the hour. Run
> `terraform destroy` when you are done.

## Prerequisites

- `gcloud` authenticated (`gcloud auth application-default login`) and a project.
- These APIs enabled: `container`, `sqladmin`, `servicenetworking`, `compute`,
  `storage`.
- `gke-gcloud-auth-plugin` installed (for kubectl auth).

## Usage

```bash
cd infra/terraform/gke
terraform init
terraform apply -var project_id=YOUR_PROJECT   # ~15–20 min

$(terraform output -raw configure_kubectl)      # point kubectl at the cluster

# ingress controller (once)
helm --namespace ingress-nginx --create-namespace install ingress-nginx \
  ingress-nginx --repo https://kubernetes.github.io/ingress-nginx

# deploy the apps — command generated with all secrets + the GCS endpoint wired in
terraform output -raw helm_install_command | bash
```

Then point DNS at the ingress LoadBalancer and add TLS. Full runbook:
[../../helm/CLOUD.md](../../helm/CLOUD.md).

## Notes

- **GCS over S3-interop.** The app's object-store client speaks S3, and GCS
  exposes an S3-compatible XML API authenticated with an HMAC key — so archives
  (PGN, analysis, opening book) read and write over `storage.googleapis.com`
  without code changes. Presigned download URLs are the one area where S3-interop
  can differ from AWS; verify the "download PGN" path in your project, or front
  those downloads through the gateway if needed. Postgres/Redis/fanout/sessions
  are unaffected.
- **Private Cloud SQL.** The DB has no public IP; it is reached over the VPC
  peering range from the GKE pods. That is why the module reserves a range and
  creates a `servicenetworking` connection.
- **Validation.** `terraform init` + `terraform validate` pass against the real
  `hashicorp/google` provider schema. It is **not** `apply`-verified — that needs
  GCP credentials and creates billable resources; apply with your own project.
