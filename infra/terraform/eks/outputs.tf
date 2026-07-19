output "region" {
  description = "AWS region."
  value       = var.region
}

output "cluster_name" {
  description = "EKS cluster name."
  value       = module.eks.cluster_name
}

output "configure_kubectl" {
  description = "Run this to point kubectl at the new cluster."
  value       = "aws eks update-kubeconfig --region ${var.region} --name ${module.eks.cluster_name}"
}

output "postgres_dsn" {
  description = "DSN for the Helm secrets.postgresDSN value."
  value       = "postgres://${var.db_username}:${random_password.db.result}@${aws_db_instance.acg.address}:5432/${var.db_name}?sslmode=require"
  sensitive   = true
}

output "s3_bucket_prefix" {
  description = "Bucket-name prefix for the Helm secrets.s3BucketPrefix value (buckets are <prefix>pgn/analysis/books)."
  value       = local.bucket_prefix
}

output "s3_endpoint" {
  description = "S3 endpoint for ACG_S3_ENDPOINT / ACG_S3_PUBLIC_ENDPOINT."
  value       = "s3.${var.region}.amazonaws.com"
}

output "s3_access_key" {
  description = "Access key for the Helm secrets.s3AccessKey value."
  value       = aws_iam_access_key.app.id
  sensitive   = true
}

output "s3_secret_key" {
  description = "Secret key for the Helm secrets.s3SecretKey value."
  value       = aws_iam_access_key.app.secret
  sensitive   = true
}

# One command that assembles the whole `helm install`, reading the sensitive
# outputs so nothing is echoed to the terminal or committed. See infra/helm/CLOUD.md.
output "helm_install_command" {
  description = "Copy-paste to deploy the apps once kubectl is configured."
  value       = <<-EOT
    helm upgrade --install acg infra/helm/alekhine \
      -f infra/helm/alekhine/values-cloud.yaml \
      --set ingress.host="chess.example.com" \
      --set services.game-service.env.ACG_S3_ENDPOINT="s3.${var.region}.amazonaws.com" \
      --set services.game-service.env.ACG_S3_PUBLIC_ENDPOINT="s3.${var.region}.amazonaws.com" \
      --set-string secrets.postgresDSN="$(terraform -chdir=infra/terraform/eks output -raw postgres_dsn)" \
      --set-string secrets.s3AccessKey="$(terraform -chdir=infra/terraform/eks output -raw s3_access_key)" \
      --set-string secrets.s3SecretKey="$(terraform -chdir=infra/terraform/eks output -raw s3_secret_key)" \
      --set-string secrets.s3BucketPrefix="$(terraform -chdir=infra/terraform/eks output -raw s3_bucket_prefix)" \
      --set secrets.sessionSecret="$(openssl rand -base64 32)" \
      --set secrets.erlangCookie="$(openssl rand -hex 24)" \
      --wait --timeout 10m
  EOT
}
