import { describe, it, expect } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { renderMentions } from "./render-mentions";

const ID = "123e4567-e89b-12d3-a456-426614174000";

function html(content: string): string {
  return renderToStaticMarkup(<>{renderMentions(content)}</>);
}

describe("renderMentions", () => {
  it("renders a member mention as an @Name chip and drops the raw token", () => {
    const out = html(`hi [@Ivan](mention://member/${ID}) there`);
    expect(out).toContain("@Ivan");
    expect(out).toContain("text-brand");
    expect(out).not.toContain("mention://");
    expect(out).toContain("hi ");
    expect(out).toContain(" there");
  });

  it("renders an agent mention", () => {
    const out = html(`[@Helper](mention://agent/${ID})`);
    expect(out).toContain("@Helper");
    expect(out).not.toContain("mention://");
  });

  it("leaves plain text untouched (no chip)", () => {
    const out = html("no mentions here");
    expect(out).toContain("no mentions here");
    expect(out).not.toContain("text-brand");
  });

  it("handles bracketed display names (e.g. David[TF])", () => {
    const out = html(`[@David[TF]](mention://member/${ID})`);
    expect(out).toContain("@David[TF]");
    expect(out).not.toContain("mention://");
  });

  it("renders multiple mentions in one string", () => {
    const out = html(`[@A](mention://member/${ID}) and [@B](mention://agent/${ID})`);
    expect(out).toContain("@A");
    expect(out).toContain("@B");
    expect(out).toContain(" and ");
    expect(out).not.toContain("mention://");
  });
});
