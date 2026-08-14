import { describe, expect, it } from "vitest";
import {
  isLocalPreviewResponse,
  rewriteLocalPreviewCookies,
} from "./preview-cookie";

describe("local product preview cookies", () => {
  it("recognizes only daemon-proxied localhost preview responses", () => {
    expect(isLocalPreviewResponse("http://127.0.0.1:19903/editor/local/51372/user/login")).toBe(true);
    expect(isLocalPreviewResponse("http://localhost:19903/editor/local/51372/")).toBe(true);
    expect(isLocalPreviewResponse("http://127.0.0.1:19903/health")).toBe(false);
    expect(isLocalPreviewResponse("https://example.com/editor/local/51372/")).toBe(false);
  });

  it("adds cross-site attributes to legacy preview session cookies", () => {
    const headers = rewriteLocalPreviewCookies(
      "http://127.0.0.1:19903/editor/local/51372/user/login",
      { "Set-Cookie": ["PHPSESSID=local-preview; path=/; HttpOnly"] },
    );

    expect(headers?.["Set-Cookie"]).toEqual([
      "PHPSESSID=local-preview; path=/; HttpOnly; Secure; SameSite=None",
    ]);
  });

  it("preserves explicit cookie policy and unrelated responses", () => {
    const explicit = { "set-cookie": ["session=value; SameSite=Strict; Secure"] };
    expect(
      rewriteLocalPreviewCookies(
        "http://127.0.0.1:19903/editor/local/51372/",
        explicit,
      ),
    ).toBe(explicit);

    const unrelated = { "Set-Cookie": ["session=value"] };
    expect(
      rewriteLocalPreviewCookies("https://agora.example.com/api/me", unrelated),
    ).toBe(unrelated);
  });
});
