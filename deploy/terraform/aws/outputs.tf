output "image_base" {
  description = "Image path without a tag (the Makefile appends :VERSION)"
  value       = module.registry.repository_url
}

output "db_host" {
  description = "RDS endpoint hostname (null until create_db)"
  value       = var.create_db ? module.database[0].address : null
}

output "db_password_secret" {
  description = "Secrets Manager ARN holding the database password (null until create_db)"
  value       = var.create_db ? module.database[0].password_secret_arn : null
}

output "serverless_url" {
  description = "ALB URL of the ECS service (null until image is set)"
  value       = var.image != "" ? module.serverless[0].url : null
}

output "k8s_cluster_name" {
  value = var.create_k8s ? module.kubernetes[0].cluster_name : null
}

output "k8s_credentials_command" {
  value = var.create_k8s ? "aws eks update-kubeconfig --name ${module.kubernetes[0].cluster_name} --region ${var.region}" : null
}
