variable "name" {
  description = "Cluster name"
  type        = string
}

variable "subnet_ids" {
  description = "Public subnets for control plane ENIs and nodes"
  type        = list(string)
}

variable "node_count" {
  description = "Desired nodes in the spot pool"
  type        = number
}

variable "node_type" {
  description = "EC2 instance type for the spot pool"
  type        = string
}

variable "db_security_group_id" {
  description = "Security group of the database; empty means no database"
  type        = string
  default     = ""
}
