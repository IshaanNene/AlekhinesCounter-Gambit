# Provisions the GCP substrate the platform runs on: a GKE cluster plus the
# managed stores the Helm chart's values-cloud.yaml expects (Cloud SQL Postgres,
# GCS buckets). The GCP analog of ../eks; Terraform owns the cluster + managed
# services, Helm owns the apps.
#
# Apply this, wire the outputs into `helm install` (see infra/helm/CLOUD.md), and
# the same chart runs on real GCP. GCS is used through its S3-compatibility (the
# app's object-store client speaks S3), authenticated with an HMAC key.
#
# NOTE: GKE, Cloud SQL, and nodes bill by the hour. Run `terraform destroy` when
# you are done.

# ── Networking (VPC-native) ──────────────────────────────────────────────────
resource "google_compute_network" "vpc" {
  name                    = "${var.name}-vpc"
  auto_create_subnetworks = false
}

resource "google_compute_subnetwork" "subnet" {
  name          = "${var.name}-subnet"
  ip_cidr_range = "10.0.0.0/20"
  region        = var.region
  network       = google_compute_network.vpc.id

  # Secondary ranges back VPC-native (alias-IP) pods and services.
  secondary_ip_range {
    range_name    = "pods"
    ip_cidr_range = "10.16.0.0/14"
  }
  secondary_ip_range {
    range_name    = "services"
    ip_cidr_range = "10.20.0.0/20"
  }
}

# ── GKE cluster ──────────────────────────────────────────────────────────────
resource "google_container_cluster" "gke" {
  name     = var.name
  location = var.region

  network    = google_compute_network.vpc.id
  subnetwork = google_compute_subnetwork.subnet.id

  # Manage the node pool separately, the idiomatic pattern.
  remove_default_node_pool = true
  initial_node_count       = 1

  ip_allocation_policy {
    cluster_secondary_range_name  = "pods"
    services_secondary_range_name = "services"
  }

  deletion_protection = false # so `terraform destroy` works for a demo
}

resource "google_container_node_pool" "primary" {
  name     = "${var.name}-pool"
  cluster  = google_container_cluster.gke.id
  location = var.region

  autoscaling {
    min_node_count = var.node_min_count
    max_node_count = var.node_max_count
  }

  node_config {
    machine_type = var.node_machine_type
    oauth_scopes = ["https://www.googleapis.com/auth/cloud-platform"]
  }
}

# ── Managed Postgres (Cloud SQL, private IP) ─────────────────────────────────
# Private Service Access: reserve a range and peer it so the DB gets a private IP
# reachable from the GKE pods without ever being exposed publicly.
resource "google_compute_global_address" "private" {
  name          = "${var.name}-sql-range"
  purpose       = "VPC_PEERING"
  address_type  = "INTERNAL"
  prefix_length = 16
  network       = google_compute_network.vpc.id
}

resource "google_service_networking_connection" "private" {
  network                 = google_compute_network.vpc.id
  service                 = "servicenetworking.googleapis.com"
  reserved_peering_ranges = [google_compute_global_address.private.name]
}

resource "random_password" "db" {
  length  = 24
  special = false # keep it URL-safe for the DSN
}

resource "google_sql_database_instance" "acg" {
  name                = "${var.name}-postgres"
  database_version    = "POSTGRES_16"
  region              = var.region
  deletion_protection = false
  depends_on          = [google_service_networking_connection.private]

  settings {
    tier = var.db_tier
    ip_configuration {
      ipv4_enabled    = false
      private_network = google_compute_network.vpc.id
    }
  }
}

resource "google_sql_database" "acg" {
  name     = var.db_name
  instance = google_sql_database_instance.acg.name
}

resource "google_sql_user" "acg" {
  name     = var.db_username
  instance = google_sql_database_instance.acg.name
  password = random_password.db.result
}

# ── Object storage (GCS via S3 interop) ──────────────────────────────────────
# GCS bucket names are globally unique, so a per-deployment prefix (fed to the app
# as ACG_S3_BUCKET_PREFIX) keeps the three logical buckets collision-free. The app
# reads them over the S3-compatible endpoint with an HMAC key.
resource "random_string" "bucket_suffix" {
  length  = 6
  special = false
  upper   = false
}

locals {
  bucket_prefix   = "${var.name}-${random_string.bucket_suffix.result}-"
  logical_buckets = ["pgn", "analysis", "books"]
}

resource "google_storage_bucket" "artifacts" {
  for_each                    = toset(local.logical_buckets)
  name                        = "${local.bucket_prefix}${each.key}"
  location                    = var.region
  uniform_bucket_level_access = true
  force_destroy               = true # so a demo tears down cleanly
}

# A service account whose HMAC key the app uses as S3 access/secret credentials.
resource "google_service_account" "app" {
  account_id   = "${var.name}-app-gcs"
  display_name = "Alekhine app GCS access"
}

resource "google_storage_bucket_iam_member" "app" {
  for_each = google_storage_bucket.artifacts
  bucket   = each.value.name
  role     = "roles/storage.objectAdmin"
  member   = "serviceAccount:${google_service_account.app.email}"
}

resource "google_storage_hmac_key" "app" {
  service_account_email = google_service_account.app.email
}
