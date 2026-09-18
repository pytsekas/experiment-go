variable "name" {
  description = "Prefix for resource names and Name tags"
  type        = string
}

variable "vpc_cidr" {
  description = "CIDR of the VPC; split into four /20 subnets"
  type        = string
}
