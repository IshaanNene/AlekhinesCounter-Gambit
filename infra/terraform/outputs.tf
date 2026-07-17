output "cluster_name" {
  description = "The kind cluster name."
  value       = kind_cluster.acg.name
}

output "kubeconfig_path" {
  description = "Path to the generated kubeconfig; export KUBECONFIG or use --kubeconfig."
  value       = kind_cluster.acg.kubeconfig_path
}

output "endpoint" {
  description = "The cluster API endpoint."
  value       = kind_cluster.acg.endpoint
}

output "ingress_url" {
  description = "Host URL mapped to the ingress-nginx controller."
  value       = "http://localhost:${var.ingress_host_port}"
}
