# Public endpoint, API authentication mode, the creator becomes cluster admin.
# Kubernetes version is left to the provider default (latest supported).
resource "aws_eks_cluster" "main" {
  name     = var.name
  role_arn = aws_iam_role.cluster.arn

  access_config {
    authentication_mode                         = "API"
    bootstrap_cluster_creator_admin_permissions = true
  }

  vpc_config {
    subnet_ids              = var.subnet_ids
    endpoint_public_access  = true
    endpoint_private_access = true
  }

  # We declare the add-ons below explicitly, so do not let EKS install
  # self-managed copies first.
  bootstrap_self_managed_addons = false

  depends_on = [aws_iam_role_policy_attachment.cluster_policy]
}

# Networking add-ons must exist before nodes join.
resource "aws_eks_addon" "vpc_cni" {
  cluster_name                = aws_eks_cluster.main.name
  addon_name                  = "vpc-cni"
  resolve_conflicts_on_create = "OVERWRITE"
  resolve_conflicts_on_update = "OVERWRITE"
}

resource "aws_eks_addon" "kube_proxy" {
  cluster_name                = aws_eks_cluster.main.name
  addon_name                  = "kube-proxy"
  resolve_conflicts_on_create = "OVERWRITE"
  resolve_conflicts_on_update = "OVERWRITE"
}

# Spot capacity: ~70% cheaper, can be reclaimed with two minutes notice, which
# is itself a good experiment (the PDB keeps one pod alive).
resource "aws_eks_node_group" "spot" {
  cluster_name    = aws_eks_cluster.main.name
  node_group_name = "spot-pool"
  node_role_arn   = aws_iam_role.node.arn
  subnet_ids      = var.subnet_ids

  capacity_type  = "SPOT"
  instance_types = [var.node_type]
  disk_size      = 20

  scaling_config {
    desired_size = var.node_count
    min_size     = 1
    max_size     = 4
  }

  update_config {
    max_unavailable = 1
  }

  depends_on = [
    aws_iam_role_policy_attachment.node,
    aws_eks_addon.vpc_cni,
    aws_eks_addon.kube_proxy,
  ]
}

# These schedule pods, so they need nodes first.
resource "aws_eks_addon" "coredns" {
  cluster_name                = aws_eks_cluster.main.name
  addon_name                  = "coredns"
  resolve_conflicts_on_create = "OVERWRITE"
  resolve_conflicts_on_update = "OVERWRITE"

  depends_on = [aws_eks_node_group.spot]
}

# Required by the HorizontalPodAutoscaler in deploy/k8s. Community add-on.
resource "aws_eks_addon" "metrics_server" {
  cluster_name                = aws_eks_cluster.main.name
  addon_name                  = "metrics-server"
  resolve_conflicts_on_create = "OVERWRITE"
  resolve_conflicts_on_update = "OVERWRITE"

  depends_on = [aws_eks_node_group.spot]
}

# Managed node groups use the cluster security group; let it reach Postgres.
resource "aws_vpc_security_group_ingress_rule" "db_from_nodes" {
  count = var.db_security_group_id != "" ? 1 : 0

  security_group_id            = var.db_security_group_id
  referenced_security_group_id = aws_eks_cluster.main.vpc_config[0].cluster_security_group_id
  from_port                    = 5432
  to_port                      = 5432
  ip_protocol                  = "tcp"
}
