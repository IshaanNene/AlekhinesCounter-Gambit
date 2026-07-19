# Provisions the AWS infrastructure the platform runs on: an EKS cluster plus the
# managed stores the Helm chart's values-cloud.yaml expects (RDS Postgres, an S3
# bucket). Terraform owns the cluster + managed services; Helm owns the apps.
#
# This is a self-contained alternative to ../main.tf (which provisions a local
# kind cluster). Apply this, wire the outputs into `helm install`
# (see infra/helm/CLOUD.md), and the same chart runs on real AWS.
#
# NOTE: creating these resources costs money (EKS control plane, NAT gateway, RDS,
# EC2 nodes). Run `terraform destroy` when you are done.

data "aws_availability_zones" "available" {
  state = "available"
}

locals {
  azs             = slice(data.aws_availability_zones.available.names, 0, var.az_count)
  private_subnets = [for i in range(var.az_count) : cidrsubnet(var.vpc_cidr, 4, i)]
  public_subnets  = [for i in range(var.az_count) : cidrsubnet(var.vpc_cidr, 4, i + 8)]
}

# ── Networking ───────────────────────────────────────────────────────────────
module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "~> 5.8"

  name = "${var.name}-vpc"
  cidr = var.vpc_cidr

  azs             = local.azs
  private_subnets = local.private_subnets
  public_subnets  = local.public_subnets

  enable_nat_gateway   = true
  single_nat_gateway   = true # one NAT keeps the demo cheap; use one-per-AZ for HA
  enable_dns_hostnames = true

  # Tags the subnets so the AWS Load Balancer / ingress controllers know where to
  # place public (internet-facing) and internal load balancers.
  public_subnet_tags  = { "kubernetes.io/role/elb" = "1" }
  private_subnet_tags = { "kubernetes.io/role/internal-elb" = "1" }
}

# ── EKS cluster ──────────────────────────────────────────────────────────────
module "eks" {
  source  = "terraform-aws-modules/eks/aws"
  version = "~> 20.8"

  cluster_name    = var.name
  cluster_version = var.cluster_version

  # Public API endpoint so you can reach it with kubectl from your laptop; nodes
  # and RDS live in private subnets.
  cluster_endpoint_public_access           = true
  enable_cluster_creator_admin_permissions = true

  vpc_id     = module.vpc.vpc_id
  subnet_ids = module.vpc.private_subnets

  # IRSA is enabled by default in v20; use it to grant the app pod-level AWS
  # permissions (the production upgrade over the static S3 keys below).
  eks_managed_node_groups = {
    default = {
      instance_types = [var.node_instance_type]
      min_size       = var.node_min_size
      max_size       = var.node_max_size
      desired_size   = var.node_desired_size
    }
  }
}

# ── Managed Postgres (RDS) ───────────────────────────────────────────────────
resource "random_password" "db" {
  length  = 24
  special = false # keep it URL-safe for the DSN
}

resource "aws_db_subnet_group" "acg" {
  name       = "${var.name}-db"
  subnet_ids = module.vpc.private_subnets
}

resource "aws_security_group" "db" {
  name        = "${var.name}-db"
  description = "Postgres access from the EKS nodes only"
  vpc_id      = module.vpc.vpc_id

  ingress {
    description     = "Postgres from EKS nodes"
    from_port       = 5432
    to_port         = 5432
    protocol        = "tcp"
    security_groups = [module.eks.node_security_group_id]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_db_instance" "acg" {
  identifier     = "${var.name}-postgres"
  engine         = "postgres"
  engine_version = "16"

  instance_class    = var.db_instance_class
  allocated_storage = var.db_allocated_storage
  storage_encrypted = true

  db_name  = var.db_name
  username = var.db_username
  password = random_password.db.result

  db_subnet_group_name   = aws_db_subnet_group.acg.name
  vpc_security_group_ids = [aws_security_group.db.id]
  publicly_accessible    = false
  multi_az               = false # single-AZ for the demo; flip on for HA

  skip_final_snapshot = true
  apply_immediately   = true
}

# ── Object storage (S3) + a scoped IAM user for the app ──────────────────────
# The app uses three logical buckets (pgn, analysis, books). On S3 bucket names
# are globally unique, so a per-deployment prefix makes them collision-free; the
# app is handed the prefix via ACG_S3_BUCKET_PREFIX and keeps using the short
# logical names. Pre-creating them here (vs. letting the app create them) keeps
# the IAM user least-privilege — no s3:CreateBucket. IRSA (above) is the keyless
# upgrade over the static credentials the app currently reads.
resource "random_string" "bucket_suffix" {
  length  = 6
  special = false
  upper   = false
}

locals {
  bucket_prefix   = "${var.name}-${random_string.bucket_suffix.result}-"
  logical_buckets = ["pgn", "analysis", "books"]
}

resource "aws_s3_bucket" "artifacts" {
  for_each = toset(local.logical_buckets)
  bucket   = "${local.bucket_prefix}${each.key}"
}

resource "aws_s3_bucket_versioning" "artifacts" {
  for_each = aws_s3_bucket.artifacts
  bucket   = each.value.id
  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_public_access_block" "artifacts" {
  for_each                = aws_s3_bucket.artifacts
  bucket                  = each.value.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_iam_user" "app" {
  name = "${var.name}-app-s3"
}

resource "aws_iam_user_policy" "app_s3" {
  name = "${var.name}-app-s3"
  user = aws_iam_user.app.name
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = ["s3:ListBucket", "s3:GetObject", "s3:PutObject", "s3:DeleteObject"]
      Resource = concat(
        [for b in aws_s3_bucket.artifacts : b.arn],
        [for b in aws_s3_bucket.artifacts : "${b.arn}/*"],
      )
    }]
  })
}

resource "aws_iam_access_key" "app" {
  user = aws_iam_user.app.name
}
