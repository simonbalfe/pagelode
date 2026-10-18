import type { ContentfulStatusCode } from "hono/utils/http-status";
import { ZodError } from "zod";

export type ErrorCode =
  | "invalid_request"
  | "unauthorized"
  | "not_found"
  | "profile_not_found"
  | "profile_in_use"
  | "identity_conflict"
  | "saturated"
  | "render_failed"
  | "shutting_down"
  | "internal";

export class ServiceError extends Error {
  readonly status: ContentfulStatusCode;
  readonly code: ErrorCode;

  constructor(status: ContentfulStatusCode, code: ErrorCode, message: string) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

export function toServiceError(error: unknown): ServiceError {
  if (error instanceof ServiceError) return error;
  if (error instanceof ZodError) {
    return new ServiceError(400, "invalid_request", error.issues.map((issue) => `${issue.path.join(".") || "request"}: ${issue.message}`).join("; "));
  }
  if (error instanceof SyntaxError) return new ServiceError(400, "invalid_request", "request body must be JSON");
  return new ServiceError(500, "internal", error instanceof Error ? error.message : "unexpected failure");
}
