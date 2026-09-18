variable "name" {
  type = string
}

variable "region" {
  description = "Region, needed by the awslogs driver"
  type        = string
}

variable "image" {
  type = string
}

variable "container_port" {
  type = number
}

variable "health_check_path" {
  description = "ALB target group health check path"
  type        = string
}

variable "env" {
  type    = map(string)
  default = {}
}

variable "secret_env" {
  description = "Environment variables sourced from Secrets Manager: name => secret ARN"
  type        = map(string)
  default     = {}
}

variable "vpc_id" {
  type = string
}

variable "public_subnet_ids" {
  type = list(string)
}

variable "db_security_group_id" {
  description = "Security group of the database; empty means no database"
  type        = string
}

variable "min_instances" {
  type = number
}

variable "max_instances" {
  type = number
}

variable "cpu" {
  description = "Fargate task CPU units, e.g. \"256\""
  type        = string
}

variable "memory" {
  description = "Fargate task memory in MiB, e.g. \"512\""
  type        = string
}
