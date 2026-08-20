locals {

  ecs_tasks = {
    # Phase 4 (04-02): voice task. Phase 5 deploy: auth task added — the auth
    # identity service is stood up so the browser client's OIDC sign-in works.
    # Phase 12 (12-07): telephony-edge task added — the deployed Asterisk
    # PSTN edge (D-01/D-04).
    enabled        = true
    enable_logging = true
    tasks          = [local.service_conf.voice.locals.task, local.service_conf.auth.locals.task, local.service_conf.telephony_edge.locals.task]
  }

  # Decoy 1: this comment mentions paused but is not an assignment -- the
  # operator pause switch (kv pause / kv resume) used to live on the next
  # line and was accidentally deleted in this fixture.

  # Decoy 2: a string literal containing the word, which must not satisfy
  # the match either.
  release_notes = "the ecs services can be paused via the operator CLI"

  ecs_services = {
    # Phase 4 (04-02): voice service. Phase 5 deploy: auth service added.
    # Phase 12 (12-07): telephony-edge service added.
    enabled = true
    services = [
      for s in [local.service_conf.voice.locals.service, local.service_conf.auth.locals.service, local.service_conf.telephony_edge.locals.service] :
      # Both overrides are required (D-16): Application Auto Scaling
      # enforces min_capacity (main.tf:313/323), so desired_count = 0 alone
      # is undone on the next scaling evaluation; min_capacity = 0 alone
      # changes nothing on its own (aws_ecs_service.service:252 still reads
      # desired_count). local.paused must drive both from one boolean.
      local.paused ? merge(s, {
        desired_count = 0
        autoscaling   = merge(s.autoscaling, { min_capacity = 0 })
      }) : s
    ]
  }
}
