# Provisions the Kubernetes cluster the platform runs on.
#
# Terraform owns the *cluster*; Helm owns the *apps* on it (see infra/helm). That
# split is deliberate and idiomatic: infrastructure and application delivery have
# different lifecycles and different tools, and in a real setup ArgoCD — not
# Terraform — drives the Helm release from git.
#
# Locally this is a kind cluster. The same shape swaps to an EKS/GKE module by
# changing only this file: the Helm chart and the deploy flow are unchanged.

terraform {
  required_version = ">= 1.5"
  required_providers {
    kind = {
      source  = "tehcyx/kind"
      version = "~> 0.9"
    }
  }
}

provider "kind" {}

resource "kind_cluster" "acg" {
  name           = var.cluster_name
  wait_for_ready = true

  kind_config {
    kind        = "Cluster"
    api_version = "kind.x-k8s.io/v1alpha4"

    # One control-plane that also runs workloads, plus workers so the HPAs have
    # somewhere to scale into and pods spread across nodes.
    node {
      role = "control-plane"

      # Publish the gateway's NodePort to the host, so the app is reachable at
      # localhost without a separate port-forward once deployed.
      extra_port_mappings {
        container_port = 30080
        host_port      = var.gateway_host_port
      }
    }

    dynamic "node" {
      for_each = range(var.worker_count)
      content {
        role = "worker"
      }
    }
  }
}
