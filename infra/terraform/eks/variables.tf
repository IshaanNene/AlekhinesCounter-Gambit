variable "region" {
  description = "AWS region to deploy into."
  type        = string
  default     = "us-east-1"
}

variable "name" {
  description = "Name prefix for all resources (cluster, VPC, RDS, bucket)."
  type        = string
  default     = "alekhine"
}

variable "cluster_version" {
  description = "EKS Kubernetes version."
  type        = string
  default     = "1.30"
}

variable "vpc_cidr" {
  description = "CIDR block for the VPC."
  type        = string
  default     = "10.0.0.0/16"
}

variable "az_count" {
  description = "Number of Availability Zones to spread subnets across."
  type        = number
  default     = 3
}

variable "node_instance_type" {
  description = "EC2 instance type for the EKS managed node group. The engine workers are CPU-bound (Stockfish), so favour compute."
  type        = string
  default     = "t3.large"
}

variable "node_min_size" {
  description = "Minimum nodes in the managed node group."
  type        = number
  default     = 2
}

variable "node_max_size" {
  description = "Maximum nodes (HPAs scale pods; the cluster-autoscaler would scale nodes into this)."
  type        = number
  default     = 5
}

variable "node_desired_size" {
  description = "Desired nodes at creation."
  type        = number
  default     = 3
}

variable "db_instance_class" {
  description = "RDS instance class for Postgres."
  type        = string
  default     = "db.t3.small"
}

variable "db_allocated_storage" {
  description = "RDS allocated storage in GiB."
  type        = number
  default     = 20
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
