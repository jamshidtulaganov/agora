import { describe, expect, it } from "vitest";
import { isStaleCookieSessionError } from "./auth-initializer";

describe("isStaleCookieSessionError", () => {
  it.each([401, 404])("treats HTTP %s as an inactive cookie session", (status) => {
    expect(isStaleCookieSessionError({ status })).toBe(true);
  });

  it.each([undefined, 400, 403, 500])("keeps HTTP %s visible as an application error", (status) => {
    expect(isStaleCookieSessionError(status === undefined ? null : { status })).toBe(false);
  });
});
