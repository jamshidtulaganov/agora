import { delimiter, resolve } from "node:path";
import { describe, it, expect } from "vitest";
import {
  builderArgsForTarget,
  classifyMacSigning,
  envWithLocalBins,
  isPublishingRelease,
  macAppPath,
  macReleaseBlockers,
  normalizeGitVersion,
  parsePackageArgs,
  resolveBuildMatrix,
  stripLeadingSeparator,
} from "./package.mjs";

describe("normalizeGitVersion", () => {
  it("returns null for empty / nullish input", () => {
    expect(normalizeGitVersion("")).toBe(null);
    expect(normalizeGitVersion(null)).toBe(null);
    expect(normalizeGitVersion(undefined)).toBe(null);
  });

  it("strips the leading v on a clean tag", () => {
    expect(normalizeGitVersion("v0.1.36")).toBe("0.1.36");
    expect(normalizeGitVersion("v1.0.0")).toBe("1.0.0");
  });

  it("preserves the prerelease suffix between tags", () => {
    expect(normalizeGitVersion("v0.1.35-14-gf1415e96")).toBe(
      "0.1.35-14-gf1415e96",
    );
  });

  it("preserves the dirty suffix on a modified worktree", () => {
    expect(normalizeGitVersion("v0.1.35-14-gf1415e96-dirty")).toBe(
      "0.1.35-14-gf1415e96-dirty",
    );
  });

  it("handles v-prefixed prerelease tags", () => {
    expect(normalizeGitVersion("v1.0.0-alpha")).toBe("1.0.0-alpha");
    expect(normalizeGitVersion("v1.0.0-rc.2")).toBe("1.0.0-rc.2");
  });

  it("falls back to 0.0.0-<hash> when no tags are reachable", () => {
    // `git describe --tags --always` returns just the short commit hash
    // when there are no tags in the history at all.
    expect(normalizeGitVersion("f1415e96")).toBe("0.0.0-f1415e96");
    expect(normalizeGitVersion("abc1234")).toBe("0.0.0-abc1234");
  });
});

describe("stripLeadingSeparator", () => {
  it("removes the leading -- inserted by npm/pnpm", () => {
    expect(stripLeadingSeparator(["--", "--mac", "--arm64", "--publish", "always"])).toEqual([
      "--mac", "--arm64", "--publish", "always",
    ]);
  });

  it("leaves args untouched when there is no leading --", () => {
    expect(stripLeadingSeparator(["--mac", "--arm64"])).toEqual(["--mac", "--arm64"]);
  });

  it("does not strip a -- that appears mid-argv", () => {
    expect(stripLeadingSeparator(["--mac", "--", "--arm64"])).toEqual([
      "--mac", "--", "--arm64",
    ]);
  });

  it("handles an empty array", () => {
    expect(stripLeadingSeparator([])).toEqual([]);
  });
});

describe("parsePackageArgs", () => {
  it("collects per-platform targets and shared args", () => {
    expect(
      parsePackageArgs([
        "--win", "nsis",
        "--mac", "dmg", "zip",
        "--arm64",
        "--publish", "never",
      ]),
    ).toEqual({
      allPlatforms: false,
      sharedArgs: ["--publish", "never"],
      platformTargets: {
        mac: ["dmg", "zip"],
        win: ["nsis"],
        linux: [],
      },
      requestedPlatforms: ["win", "mac"],
      requestedArchs: ["arm64"],
    });
  });

  it("expands combined short flags", () => {
    expect(parsePackageArgs(["-mw", "--x64"]).requestedPlatforms).toEqual([
      "mac",
      "win",
    ]);
  });

  it("tracks the all-platforms shortcut", () => {
    expect(parsePackageArgs(["--all-platforms", "--publish", "never"]).allPlatforms).toBe(true);
  });
});

describe("resolveBuildMatrix", () => {
  it("defaults to the current host platform and arch", () => {
    expect(
      resolveBuildMatrix(
        {
          allPlatforms: false,
          sharedArgs: [],
          platformTargets: { mac: [], win: [], linux: [] },
          requestedPlatforms: [],
          requestedArchs: [],
        },
        "darwin",
        "arm64",
      ),
    ).toEqual([{ platform: "mac", arch: "arm64" }]);
  });

  it("expands all-platforms on macOS", () => {
    expect(
      resolveBuildMatrix(
        {
          allPlatforms: true,
          sharedArgs: [],
          platformTargets: { mac: [], win: [], linux: [] },
          requestedPlatforms: [],
          requestedArchs: [],
        },
        "darwin",
        "arm64",
      ),
    ).toEqual([
      { platform: "mac", arch: "arm64" },
      { platform: "win", arch: "x64" },
      { platform: "win", arch: "arm64" },
      { platform: "linux", arch: "x64" },
      { platform: "linux", arch: "arm64" },
    ]);
  });

  it("rejects unsupported architectures", () => {
    expect(() =>
      resolveBuildMatrix(
        {
          allPlatforms: false,
          sharedArgs: [],
          platformTargets: { mac: [], win: [], linux: [] },
          requestedPlatforms: ["win"],
          requestedArchs: ["universal"],
        },
        "darwin",
        "arm64",
      ),
    ).toThrow(/unsupported Desktop CLI architecture/);
  });
});

describe("builderArgsForTarget", () => {
  it("adds scoped output directories for multi-target builds", () => {
    expect(
      builderArgsForTarget(
        { platform: "win", arch: "arm64" },
        {
          allPlatforms: false,
          sharedArgs: ["--publish", "never"],
          platformTargets: { mac: [], win: ["nsis"], linux: [] },
          requestedPlatforms: ["win"],
          requestedArchs: ["arm64"],
        },
        "1.2.3",
        {
          disableMacNotarize: true,
          hostPlatform: "darwin",
          useScopedOutputDir: true,
        },
      ),
    ).toEqual([
      "-c.extraMetadata.version=1.2.3",
      "-c.mac.notarize=false",
      "--win",
      "nsis",
      "--arm64",
      "--publish",
      "never",
      "-c.directories.output=dist/win-arm64",
      "-c.publish.channel=latest-arm64",
    ]);
  });

  it("does not override the publish channel for Windows x64 (default latest.yml)", () => {
    expect(
      builderArgsForTarget(
        { platform: "win", arch: "x64" },
        {
          allPlatforms: false,
          sharedArgs: ["--publish", "always"],
          platformTargets: { mac: [], win: ["nsis"], linux: [] },
          requestedPlatforms: ["win"],
          requestedArchs: ["x64"],
        },
        "1.2.3",
        { hostPlatform: "win32", useScopedOutputDir: true },
      ),
    ).toEqual([
      "-c.extraMetadata.version=1.2.3",
      "--win",
      "nsis",
      "--x64",
      "--publish",
      "always",
      "-c.directories.output=dist/win-x64",
    ]);
  });

  it("defaults linux cross-builds to AppImage on non-Linux hosts", () => {
    expect(
      builderArgsForTarget(
        { platform: "linux", arch: "x64" },
        {
          allPlatforms: false,
          sharedArgs: ["--publish", "never"],
          platformTargets: { mac: [], win: [], linux: [] },
          requestedPlatforms: ["linux"],
          requestedArchs: ["x64"],
        },
        "1.2.3",
        { hostPlatform: "darwin" },
      ),
    ).toEqual([
      "-c.extraMetadata.version=1.2.3",
      "--linux",
      "AppImage",
      "--x64",
      "--publish",
      "never",
    ]);
  });
});

describe("envWithLocalBins", () => {
  it("prepends desktop-local binary directories to PATH", () => {
    const desktopRoot = "/repo/apps/desktop";
    const result = envWithLocalBins(
      { PATH: ["/usr/local/bin", "/usr/bin"].join(delimiter) },
      desktopRoot,
    );
    expect(result.PATH.split(delimiter)).toEqual([
      resolve(desktopRoot, "node_modules", ".bin"),
      resolve(desktopRoot, "..", "..", "node_modules", ".bin"),
      "/usr/local/bin",
      "/usr/bin",
    ]);
  });

  it("preserves an existing Path key and avoids duplicate entries", () => {
    const desktopRoot = "/repo/apps/desktop";
    const desktopBin = resolve(desktopRoot, "node_modules", ".bin");
    const workspaceBin = resolve(desktopRoot, "..", "..", "node_modules", ".bin");
    const result = envWithLocalBins(
      { Path: [desktopBin, "runner-bin", workspaceBin].join(delimiter) },
      desktopRoot,
    );
    expect(result).not.toHaveProperty("PATH");
    expect(result.Path.split(delimiter)).toEqual([
      desktopBin,
      workspaceBin,
      "runner-bin",
    ]);
  });
});

describe("isPublishingRelease", () => {
  it("is false for a local build with no publish flag", () => {
    expect(isPublishingRelease([])).toBe(false);
    expect(isPublishingRelease(["--dir"])).toBe(false);
  });

  it("is false for an explicit --publish never", () => {
    expect(isPublishingRelease(["--publish", "never"])).toBe(false);
    expect(isPublishingRelease(["--publish=never"])).toBe(false);
    expect(isPublishingRelease(["-p", "never"])).toBe(false);
  });

  it("is true for a real release", () => {
    expect(isPublishingRelease(["--publish", "always"])).toBe(true);
    expect(isPublishingRelease(["--publish=always"])).toBe(true);
    expect(isPublishingRelease(["-p", "onTag"])).toBe(true);
  });

  it("treats a bare trailing --publish as publishing", () => {
    expect(isPublishingRelease(["--publish"])).toBe(true);
    expect(isPublishingRelease(["--publish", "--dir"])).toBe(true);
  });
});

describe("macAppPath", () => {
  it("uses the arch-suffixed directory electron-builder emits", () => {
    expect(macAppPath({ platform: "mac", arch: "arm64" })).toBe(
      "dist/mac-arm64/Agora.app",
    );
    expect(macAppPath({ platform: "mac", arch: "universal" })).toBe(
      "dist/mac-universal/Agora.app",
    );
  });

  it("uses the bare `mac` directory for x64", () => {
    expect(macAppPath({ platform: "mac", arch: "x64" })).toBe("dist/mac/Agora.app");
  });

  it("nests inside a scoped output dir for multi-target builds", () => {
    expect(macAppPath({ platform: "mac", arch: "arm64" }, "dist/mac-arm64")).toBe(
      "dist/mac-arm64/mac-arm64/Agora.app",
    );
  });
});

describe("classifyMacSigning", () => {
  // Verbatim `codesign -dv --verbose=2` output from the ad-hoc 0.3.49 build
  // that shipped a permanently broken auto-update.
  const ADHOC_CODESIGN = [
    "Executable=/Applications/Agora.app/Contents/MacOS/Agora",
    "Identifier=ai.agora.desktop",
    "Format=app bundle with Mach-O thin (arm64)",
    'CodeDirectory v=20500 size=433 flags=0x10002(adhoc,runtime) hashes=3+7 location=embedded',
    "Signature=adhoc",
    "Info.plist entries=33",
    "TeamIdentifier=not set",
  ].join("\n");

  const DEVELOPER_ID_CODESIGN = [
    "Executable=/Applications/Agora.app/Contents/MacOS/Agora",
    "Identifier=ai.agora.desktop",
    "Signature size=9000",
    "Authority=Developer ID Application: Agora (ABCDE12345)",
    "Authority=Developer ID Certification Authority",
    "Authority=Apple Root CA",
    "TeamIdentifier=ABCDE12345",
  ].join("\n");

  const DEVELOPER_ID_REQUIREMENT =
    'designated => identifier "ai.agora.desktop" and anchor apple generic and ' +
    "certificate leaf[subject.OU] = ABCDE12345";

  it("rejects the ad-hoc signature that broke 0.3.49→0.3.50", () => {
    const verdict = classifyMacSigning({
      codesignOutput: ADHOC_CODESIGN,
      requirementOutput: '# designated => cdhash H"2d6e2a50cb584028721f2a4ec287c3e8"',
    });
    expect(verdict.ok).toBe(false);
    expect(verdict.reason).toMatch(/ad-hoc/);
  });

  it("rejects a bundle that is not signed at all", () => {
    const verdict = classifyMacSigning({
      codesignOutput: "Agora.app: code object is not signed at all",
    });
    expect(verdict.ok).toBe(false);
    expect(verdict.reason).toMatch(/not signed at all/);
  });

  it("rejects a signature that is not Developer ID", () => {
    const verdict = classifyMacSigning({
      codesignOutput: [
        "Signature size=9000",
        "Authority=Apple Development: someone (ABCDE12345)",
        "TeamIdentifier=ABCDE12345",
      ].join("\n"),
      requirementOutput: DEVELOPER_ID_REQUIREMENT,
    });
    expect(verdict.ok).toBe(false);
    expect(verdict.reason).toMatch(/Developer ID Application certificate/);
  });

  it("rejects a cdhash-pinned designated requirement even if the authority looks right", () => {
    const verdict = classifyMacSigning({
      codesignOutput: DEVELOPER_ID_CODESIGN,
      requirementOutput: 'designated => cdhash H"2d6e2a50cb584028721f2a4ec287c3e8"',
    });
    expect(verdict.ok).toBe(false);
    expect(verdict.reason).toMatch(/cdhash/);
  });

  it("accepts a Developer ID signature with a stable designated requirement", () => {
    expect(
      classifyMacSigning({
        codesignOutput: DEVELOPER_ID_CODESIGN,
        requirementOutput: DEVELOPER_ID_REQUIREMENT,
      }),
    ).toEqual({ ok: true });
  });

  it("rejects empty codesign output rather than defaulting to OK", () => {
    expect(classifyMacSigning({}).ok).toBe(false);
    expect(classifyMacSigning().ok).toBe(false);
  });
});

describe("macReleaseBlockers", () => {
  it("flags the forced ad-hoc signature that shipped the broken 0.3.49 build", () => {
    expect(
      macReleaseBlockers({
        CSC_IDENTITY_AUTO_DISCOVERY: "false",
        APPLE_TEAM_ID: "ABCDE12345",
      }),
    ).toEqual([expect.stringMatching(/CSC_IDENTITY_AUTO_DISCOVERY/)]);
  });

  it("flags a missing APPLE_TEAM_ID", () => {
    expect(macReleaseBlockers({})).toEqual([
      expect.stringMatching(/APPLE_TEAM_ID/),
    ]);
  });

  it("reports both problems at once rather than one at a time", () => {
    expect(macReleaseBlockers({ CSC_IDENTITY_AUTO_DISCOVERY: "false" })).toHaveLength(2);
  });

  it("passes a properly configured release environment", () => {
    expect(macReleaseBlockers({ APPLE_TEAM_ID: "ABCDE12345" })).toEqual([]);
  });
});
