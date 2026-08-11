import { describe, expect, it } from "vitest";
import { parseWithFallback } from "../api/schema";
import {
  BitrixImportResponseSchema,
  EMPTY_BITRIX_IMPORT_RESPONSE,
} from "./types";

describe("BitrixImportResponseSchema", () => {
  it("parses an asynchronous import response", () => {
    const parsed = parseWithFallback(
      { accepted: 12, errors: null, server_version: "next" },
      BitrixImportResponseSchema,
      EMPTY_BITRIX_IMPORT_RESPONSE,
      { endpoint: "POST /api/bitrix/import/mine" },
    );

    expect(parsed).toMatchObject({
      created: 0,
      updated: 0,
      skipped: 0,
      accepted: 12,
      errors: [],
    });
  });

  it("falls back safely for a malformed response", () => {
    const parsed = parseWithFallback(
      { accepted: "twelve", errors: "bad" },
      BitrixImportResponseSchema,
      EMPTY_BITRIX_IMPORT_RESPONSE,
      { endpoint: "POST /api/bitrix/import/mine" },
    );

    expect(parsed).toEqual(EMPTY_BITRIX_IMPORT_RESPONSE);
  });
});
