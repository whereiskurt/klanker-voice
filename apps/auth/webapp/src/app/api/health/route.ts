import { NextResponse } from "next/server";

/**
 * Health check endpoint (net-new, Plan 03-01).
 *
 * Matches the ALB target group health_check_path declared in
 * infra/terraform/live/site/services/auth/service.hcl ("/api/health").
 * Intentionally has zero dependencies (no DynamoDB/SES calls) so it reflects
 * container liveness, not downstream availability.
 */
export async function GET() {
  return NextResponse.json({ status: "ok" }, { status: 200 });
}
