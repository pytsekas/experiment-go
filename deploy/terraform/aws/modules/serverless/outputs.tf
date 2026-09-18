output "url" {
  value = "http://${aws_lb.main.dns_name}"
}

output "task_security_group_id" {
  value = aws_security_group.task.id
}
