import { describe, expect, it } from "vitest";

import {
  getGroupOpenAIServiceTier,
  groupOpenAIServiceTierToAPI,
  normalizeGroupOpenAIFast,
  supportsGroupOpenAIFast,
} from "../groupsOpenAIFast";
import en from "@/i18n/locales/en/admin/overview";
import zh from "@/i18n/locales/zh/admin/overview";

describe("groupsOpenAIFast", () => {
  it("supports OpenAI and composite groups", () => {
    expect(supportsGroupOpenAIFast("openai")).toBe(true);
    expect(supportsGroupOpenAIFast("composite")).toBe(true);
    expect(supportsGroupOpenAIFast("anthropic")).toBe(false);
  });

  it("clears stale enabled state on unsupported platforms", () => {
    expect(normalizeGroupOpenAIFast("openai", true)).toBe(true);
    expect(normalizeGroupOpenAIFast("composite", true)).toBe(true);
    expect(normalizeGroupOpenAIFast("anthropic", true)).toBe(false);
    expect(normalizeGroupOpenAIFast("openai", false)).toBe(false);
  });

  it("reads legacy Fast groups and honors an explicitly cleared tier", () => {
    expect(getGroupOpenAIServiceTier({ platform: "openai", force_openai_fast: true })).toBe("priority");
    expect(getGroupOpenAIServiceTier({ platform: "openai", force_openai_fast: true, force_openai_service_tier: "" })).toBe("");
    expect(getGroupOpenAIServiceTier({ platform: "openai", force_openai_fast: true, force_openai_service_tier: "ultrafast" })).toBe("ultrafast");
  });

  it("keeps the legacy flag synchronized and clears unsupported platforms", () => {
    expect(groupOpenAIServiceTierToAPI("openai", "priority")).toEqual({ force_openai_service_tier: "priority", force_openai_fast: true });
    expect(groupOpenAIServiceTierToAPI("composite", "ultrafast")).toEqual({ force_openai_service_tier: "ultrafast", force_openai_fast: false });
    expect(groupOpenAIServiceTierToAPI("anthropic", "ultrafast")).toEqual({ force_openai_service_tier: "", force_openai_fast: false });
  });

  it("provides localized tier labels and states protocol and billing limits", () => {
    expect(zh.groups.openaiFast.hint).toContain("Excel BPS");
    expect(en.groups.openaiFast.hint).toContain("Excel BPS");
    expect(zh.groups.openaiFast).toMatchObject({
      title: expect.any(String),
      force: expect.any(String),
      noForce: expect.any(String),
      priority: expect.stringContaining("priority"),
      ultrafast: "Ultrafast",
      hint: expect.stringContaining("WebSocket"),
      free: expect.any(String),
      freeHint: expect.stringContaining("Ultrafast"),
    });
    expect(en.groups.openaiFast).toMatchObject({
      title: expect.any(String),
      force: expect.any(String),
      noForce: expect.any(String),
      priority: expect.stringContaining("priority"),
      ultrafast: "Ultrafast",
      hint: expect.stringContaining("WebSocket"),
      free: expect.any(String),
      freeHint: expect.stringContaining("Ultrafast"),
    });
  });
});
