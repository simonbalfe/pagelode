import { describe, expect, test } from "bun:test";
import { renderRequestSchema } from "./contract";

describe("render request", () => {
  test("applies safe defaults", () => {
    const parsed = renderRequestSchema.parse({ id: "1", url: "https://example.com" });
    expect(parsed.timeout_ms).toBe(45_000);
    expect(parsed.wait_until).toBe("domcontentloaded");
    expect(parsed.settle_challenge).toBe(true);
    expect(parsed.cookies).toEqual([]);
  });

  test("rejects non-HTTP targets", () => {
    expect(() => renderRequestSchema.parse({ id: "1", url: "file:///etc/passwd" })).toThrow();
  });

  test("requires a correlation id", () => {
    expect(() => renderRequestSchema.parse({ url: "https://example.com" })).toThrow();
  });
});
