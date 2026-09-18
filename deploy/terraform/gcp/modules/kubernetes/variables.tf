variable "project_id" {
  type = string
}

variable "zone" {
  description = "Zone of the (zonal) cluster"
  type        = string
}

variable "name" {
  description = "Cluster name"
  type        = string
}

variable "node_count" {
  description = "Initial number of nodes in the spot pool"
  type        = number
}

variable "node_type" {
  description = "Machine type for the spot pool"
  type        = string
}

variable "k8s_namespace" {
  description = "Namespace of the app (must match deploy/k8s)"
  type        = string
}

variable "k8s_service_account" {
  description = "Kubernetes ServiceAccount of the app (must match deploy/k8s)"
  type        = string
}
