# DISABLED by default: CHVServer.Close stops live VMs; do not enable unattended.
variable "enabled" { type = bool default = false }
variable "node_name" { type = string }
variable "version" { type = string }
job "isodo-node" {
  datacenters = ["dc1"]
  type = "service"
  group "compute" {
    count = var.enabled ? 1 : 0
    constraint { attribute = "${node.unique.name}" value = var.node_name }
    network { mode = "host" port "rpc" { static = 9090 } }
    restart { attempts = 0 mode = "fail" }
    reschedule { attempts = 0 unlimited = false }
    task "node" {
      driver = "raw_exec"
      config { command = "/opt/isodo/bin/run-node" args = [var.version] }
      resources { cpu = 1000 memory = 2048 }
    }
  }
}
