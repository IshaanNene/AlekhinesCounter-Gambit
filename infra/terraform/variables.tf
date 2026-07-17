variable "cluster_name" {
  description = "Name of the kind cluster."
  type        = string
  default     = "alekhine"
}

variable "worker_count" {
  description = "Number of worker nodes. Keep small on a laptop."
  type        = number
  default     = 2
}

variable "gateway_host_port" {
  description = "Host port the gateway NodePort is published on."
  type        = number
  default     = 8088
}
