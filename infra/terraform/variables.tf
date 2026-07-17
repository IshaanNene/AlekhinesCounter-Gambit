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

variable "ingress_host_port" {
  description = "Host port mapped to the ingress-nginx controller (container port 80)."
  type        = number
  default     = 8888
}
