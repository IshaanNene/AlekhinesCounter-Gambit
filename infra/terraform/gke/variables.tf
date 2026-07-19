variable "project_id" {
  description = "GCP project ID to deploy into (required)."
  type        = string
}

variable "region" {
  description = "GCP region."
  type        = string
  default     = "us-central1"
}

variable "name" {
  description = "Name prefix for all resources (cluster, network, Cloud SQL, buckets)."
  type        = string
  default     = "alekhine"
}

variable "node_machine_type" {
  description = "Machine type for the GKE node pool. The engine workers are CPU-bound (Stockfish), so favour compute."
  type        = string
  default     = "e2-standard-2"
}

variable "node_min_count" {
  description = "Minimum nodes per zone in the node pool."
  type        = number
  default     = 1
}

variable "node_max_count" {
  description = "Maximum nodes per zone (autoscaling ceiling)."
  type        = number
  default     = 3
}

variable "db_tier" {
  description = "Cloud SQL machine tier for Postgres."
  type        = string
  default     = "db-g1-small"
}

variable "db_username" {
  description = "Master username for the Postgres database."
  type        = string
  default     = "acg"
}

variable "db_name" {
  description = "Initial database name."
  type        = string
  default     = "acg"
}
