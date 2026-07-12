data "aws_ec2_managed_prefix_list" "cloudfront" {
  name = "com.amazonaws.global.cloudfront.origin-facing"
}

# Security Group: SSH and HTTPS
resource "aws_security_group" "sshhttps" {
  name        = "${var.region.label}.${var.dns.zonename}-securemgmt"
  description = "Allow TLS inbound traffic"
  vpc_id      = aws_vpc.vpc.id

  ingress = [
    {
      description      = "HTTPS port to VPC"
      from_port        = 443
      to_port          = 443
      protocol         = "tcp"
      cidr_blocks      = []
      ipv6_cidr_blocks = []
      self             = true
      prefix_list_ids  = [data.aws_ec2_managed_prefix_list.cloudfront.id]
      security_groups  = []
    },
    {
      description      = "SSH port to VPC"
      from_port        = 22
      to_port          = 22
      protocol         = "tcp"
      cidr_blocks      = ["0.0.0.0/0"]
      ipv6_cidr_blocks = []
      self             = true
      prefix_list_ids  = []
      security_groups  = []
    }
  ]

  egress = [
    {
      description      = "All outbound from VPC"
      from_port        = 0
      to_port          = 0
      protocol         = "-1"
      cidr_blocks      = ["0.0.0.0/0"]
      ipv6_cidr_blocks = ["::/0"]
      self             = true
      prefix_list_ids  = []
      security_groups  = []
    }
  ]

  tags = merge(
    var.vpc.tags,
    {
      Name = "${var.region.label}.${var.dns.zonename}-securemgmt"
    }
  )
}

# Security Group: HTTP Only
resource "aws_security_group" "http_only" {
  name        = "${var.region.label}.${var.dns.zonename}-http_only"
  description = "Allow HTTP inbound traffic for certbot setup"
  vpc_id      = aws_vpc.vpc.id

  ingress = [
    {
      description      = "HTTP port to VPC"
      from_port        = 80
      to_port          = 80
      protocol         = "tcp"
      cidr_blocks      = ["0.0.0.0/0"]
      ipv6_cidr_blocks = []
      self             = true
      prefix_list_ids  = []
      security_groups  = []
    },
    {
      # Phase 4 (04-03 deploy checkpoint): public HTTPS on the internet-facing ALB.
      # Phase 2 left the 443 listener's ingress mgmt-only ("TLS handshake deferred to
      # Phase 4 by design", STATE.md); voice.klankermaker.ai is a public endpoint, so
      # 443 must accept the internet. Tasks in this SG listen on 7860, not 443.
      description      = "HTTPS port to public (ALB 443 listener)"
      from_port        = 443
      to_port          = 443
      protocol         = "tcp"
      cidr_blocks      = ["0.0.0.0/0"]
      ipv6_cidr_blocks = []
      self             = true
      prefix_list_ids  = []
      security_groups  = []
    },
    {
      description      = "HTTP port 8080 to VPC"
      from_port        = 8080
      to_port          = 8080
      protocol         = "tcp"
      cidr_blocks      = []
      ipv6_cidr_blocks = []
      self             = true
      prefix_list_ids  = []
      security_groups  = []
    },
    {
      description      = "HTTP port 3000 to VPC"
      from_port        = 3000
      to_port          = 3000
      protocol         = "tcp"
      cidr_blocks      = []
      ipv6_cidr_blocks = []
      self             = true
      prefix_list_ids  = []
      security_groups  = []
    },
    {
      description      = "Strapi CMS port 1337 to VPC"
      from_port        = 1337
      to_port          = 1337
      protocol         = "tcp"
      cidr_blocks      = []
      ipv6_cidr_blocks = []
      self             = true
      prefix_list_ids  = []
      security_groups  = []
    },
    {
      # Phase 4 (04-03 deploy checkpoint): the voice Pipecat container listens on
      # 7860 for the ALB health check (/health) and /api/offer signaling. The ALB
      # shares this SG, so a self-referencing 7860 rule lets ALB -> task reach it
      # (media stays on the separate webrtc-udp SG, 20000-20100).
      description      = "Voice service port 7860 (ALB to Pipecat /api/offer and /health)"
      from_port        = 7860
      to_port          = 7860
      protocol         = "tcp"
      cidr_blocks      = []
      ipv6_cidr_blocks = []
      self             = true
      prefix_list_ids  = []
      security_groups  = []
    }
  ]

  tags = merge(
    var.vpc.tags,
    {
      Name = "${var.region.label}.${var.dns.zonename}-http_only"
    }
  )
}

# Security Group: PostgreSQL
resource "aws_security_group" "postgres" {
  name        = "${var.region.label}.${var.dns.zonename}-postgres"
  description = "PostgreSQL internal security group"
  vpc_id      = aws_vpc.vpc.id

  ingress {
    description     = "PostgreSQL port from HTTP and HTTPS security groups"
    from_port       = 5432
    to_port         = 5432
    protocol        = "tcp"
    security_groups = [aws_security_group.http_only.id, aws_security_group.sshhttps.id]
    self            = true
  }

  egress {
    description = "Allow all outbound traffic for database updates and replication"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = merge(
    var.vpc.tags,
    {
      Name = "${var.region.label}.${var.dns.zonename}-postgres"
    }
  )
}

# Security Group: Etherpad
resource "aws_security_group" "etherpad" {
  name        = "${var.region.label}.${var.dns.zonename}-etherpad"
  description = "Etherpad internal security group"
  vpc_id      = aws_vpc.vpc.id

  ingress {
    description     = "Etherpad port from HTTP and HTTPS security groups"
    from_port       = 9001
    to_port         = 9001
    protocol        = "tcp"
    security_groups = [aws_security_group.http_only.id, aws_security_group.sshhttps.id]
    self            = true
  }

  egress {
    description = "Allow all outbound traffic for Etherpad dependencies"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = merge(
    var.vpc.tags,
    {
      Name = "${var.region.label}.${var.dns.zonename}-etherpad"
    }
  )
}

# Security Group: WebRTC UDP media
# Phase 4 (D-12/T-04-06): narrowed from the Phase-2 groundwork's wide
# 1024-65535 ephemeral range to the bounded 20000-20100 media window. The
# voice task's container sysctl (net.ipv4.ip_local_port_range) pins aiortc's
# OS-ephemeral UDP bind range to this same window so the open ingress
# surface matches exactly what WebRTC media needs.
resource "aws_security_group" "webrtc_udp" {
  name        = "${var.region.label}.${var.dns.zonename}-webrtc-udp"
  description = "WebRTC UDP media ingress for voice tasks"
  vpc_id      = aws_vpc.vpc.id

  ingress {
    description = "WebRTC media UDP range (20000-20100, D-12)"
    from_port   = 20000
    to_port     = 20100
    protocol    = "udp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  egress {
    description = "Allow all outbound traffic"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = merge(
    var.vpc.tags,
    {
      Name = "${var.region.label}.${var.dns.zonename}-webrtc-udp"
    }
  )
}

# Security Group: telephony-edge (Phase 12, D-01/T-12-07-01)
#
# Inbound-only Asterisk PSTN edge (the VoIP.ms registration trunk). Ingress
# is locked to var.telephony_edge_pop_cidrs — the eight Toronto VoIP.ms POP
# /32s (network.hcl -> telephony-sg.hcl), NEVER 0.0.0.0/0. `dynamic` blocks
# with an empty CIDR list produce ZERO ingress rules (fully closed), so a
# site/region that doesn't set telephony_edge_pop_cidrs (the default
# everywhere except us-east-1) gets a security group with no way in at all,
# not an accidentally-open one.
#
# Deliberately a STANDALONE resource, not part of the default
# security_group_ids list output below — see that output's own comment.
resource "aws_security_group" "telephony_edge" {
  name        = "${var.region.label}.${var.dns.zonename}-telephony-edge"
  description = "Asterisk telephony edge: SIP/RTP ingress from VoIP.ms Toronto POPs only (D-01)"
  vpc_id      = aws_vpc.vpc.id

  dynamic "ingress" {
    for_each = var.telephony_edge_pop_cidrs
    content {
      description = "VoIP.ms Toronto POP SIP signaling (PJSIP transport-udp)"
      from_port   = 5060
      to_port     = 5060
      protocol    = "udp"
      cidr_blocks = [ingress.value]
    }
  }

  dynamic "ingress" {
    for_each = var.telephony_edge_pop_cidrs
    content {
      description = "VoIP.ms Toronto POP RTP media (rtp.conf 20000-20100 range)"
      from_port   = 20000
      to_port     = 20100
      protocol    = "udp"
      cidr_blocks = [ingress.value]
    }
  }

  egress {
    description = "Allow all outbound traffic (registration, DNS, RTP return path)"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = merge(
    var.vpc.tags,
    {
      Name = "${var.region.label}.${var.dns.zonename}-telephony-edge"
    }
  )
}

# Security Group: NLB (MQTT)
resource "aws_security_group" "nlb" {
  name        = "${var.region.label}.${var.dns.zonename}-mqtt-nlb"
  description = "MQTT rules for TLS and regular traffic"
  vpc_id      = aws_vpc.vpc.id

  ingress = [
    {
      description      = "TLS MQTT"
      from_port        = 8883
      to_port          = 8883
      protocol         = "tcp"
      cidr_blocks      = ["0.0.0.0/0"]
      ipv6_cidr_blocks = []
      self             = true
      prefix_list_ids  = []
      security_groups  = []
    },
    {
      description      = "TLS MQTT (Meshtastic default)"
      from_port        = 4433
      to_port          = 4433
      protocol         = "tcp"
      cidr_blocks      = ["0.0.0.0/0"]
      ipv6_cidr_blocks = []
      self             = true
      prefix_list_ids  = []
      security_groups  = []
    },
    {
      description      = "MQTT"
      from_port        = 1883
      to_port          = 1883
      protocol         = "tcp"
      cidr_blocks      = ["0.0.0.0/0"]
      ipv6_cidr_blocks = []
      self             = true
      prefix_list_ids  = []
      security_groups  = []
    },
    {
      description      = "Websocket-MQTT"
      from_port        = 9001
      to_port          = 9001
      protocol         = "tcp"
      cidr_blocks      = []
      ipv6_cidr_blocks = []
      self             = true
      prefix_list_ids  = []
      security_groups  = []
    },
    {
      description      = "TLS-WebSocket-MQTT"
      from_port        = 8443
      to_port          = 8443
      protocol         = "tcp"
      cidr_blocks      = ["0.0.0.0/0"]
      ipv6_cidr_blocks = []
      self             = true
      prefix_list_ids  = []
      security_groups  = []
    },
    {
      description      = "HTTPS"
      from_port        = 443
      to_port          = 443
      protocol         = "tcp"
      cidr_blocks      = ["0.0.0.0/0"]
      ipv6_cidr_blocks = []
      self             = true
      prefix_list_ids  = []
      security_groups  = []
    }
  ]

  egress {
    description = "Allow all outbound traffic for MQTT broker"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = merge(
    var.vpc.tags,
    {
      Name = "${var.region.label}.${var.dns.zonename}-mqtt-nlb"
    }
  )
}
