output "region" {
  description = "GCP region."
  value       = var.region
}

output "cluster_name" {
  description = "GKE cluster name."
  value       = google_container_cluster.gke.name
}

output "configure_kubectl" {
  description = "Run this to point kubectl at the new cluster (needs the gke-gcloud-auth-plugin)."
  value       = "gcloud container clusters get-credentials ${google_container_cluster.gke.name} --region ${var.region} --project ${var.project_id}"
}

output "postgres_dsn" {
  description = "DSN for the Helm secrets.postgresDSN value (Cloud SQL private IP)."
  value       = "postgres://${var.db_username}:${random_password.db.result}@${google_sql_database_instance.acg.private_ip_address}:5432/${var.db_name}?sslmode=require"
  sensitive   = true
}

output "s3_endpoint" {
  description = "GCS S3-compatibility endpoint for ACG_S3_ENDPOINT / ACG_S3_PUBLIC_ENDPOINT."
  value       = "storage.googleapis.com"
}

output "s3_access_key" {
  description = "HMAC access id for the Helm secrets.s3AccessKey value."
  value       = google_storage_hmac_key.app.access_id
  sensitive   = true
}

output "s3_secret_key" {
  description = "HMAC secret for the Helm secrets.s3SecretKey value."
  value       = google_storage_hmac_key.app.secret
  sensitive   = true
}

output "s3_bucket_prefix" {
  description = "Bucket-name prefix for secrets.s3BucketPrefix (buckets are <prefix>pgn/analysis/books)."
  value       = local.bucket_prefix
}

# Assembles the whole `helm install`, reading the sensitive outputs so nothing is
# echoed or committed, and pointing object storage at GCS. See infra/helm/CLOUD.md.
output "helm_install_command" {
  description = "Copy-paste to deploy the apps once kubectl is configured."
  value       = <<-EOT
    helm upgrade --install acg infra/helm/alekhine \
      -f infra/helm/alekhine/values-cloud.yaml \
      --set ingress.host="chess.example.com" \
      --set services.game-service.env.ACG_S3_ENDPOINT="storage.googleapis.com" \
      --set services.game-service.env.ACG_S3_PUBLIC_ENDPOINT="storage.googleapis.com" \
      --set services.engine-worker.env.ACG_S3_ENDPOINT="storage.googleapis.com" \
      --set services.analysis-worker.env.ACG_S3_ENDPOINT="storage.googleapis.com" \
      --set-string secrets.postgresDSN="$(terraform -chdir=infra/terraform/gke output -raw postgres_dsn)" \
      --set-string secrets.s3AccessKey="$(terraform -chdir=infra/terraform/gke output -raw s3_access_key)" \
      --set-string secrets.s3SecretKey="$(terraform -chdir=infra/terraform/gke output -raw s3_secret_key)" \
      --set-string secrets.s3BucketPrefix="$(terraform -chdir=infra/terraform/gke output -raw s3_bucket_prefix)" \
      --set secrets.sessionSecret="$(openssl rand -base64 32)" \
      --set secrets.erlangCookie="$(openssl rand -hex 24)" \
      --wait --timeout 10m
  EOT
}
