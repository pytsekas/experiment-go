output "address" {
  description = "RDS endpoint hostname"
  value       = aws_db_instance.main.address
}

output "port" {
  value = aws_db_instance.main.port
}

output "database" {
  value = aws_db_instance.main.db_name
}

output "user" {
  value = aws_db_instance.main.username
}

output "password" {
  value     = random_password.db.result
  sensitive = true
}

output "password_secret_arn" {
  description = "Secrets Manager secret holding the database password"
  value       = aws_secretsmanager_secret.db_password.arn
}

output "security_group_id" {
  description = "Security group of the instance; consumers add ingress rules to it"
  value       = aws_security_group.db.id
}
