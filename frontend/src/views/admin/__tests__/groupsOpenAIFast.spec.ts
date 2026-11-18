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

  it("reads legacy flags and gives a nonempty explicit tier precedence", () => {
    expect(getGroupOpenAIServiceTier({ platform: "openai", force_openai_fast: true })).toBe("priority");
    expect(getGroupOpenAIServiceTier({ platform: "openai", force_openai_fast: true, force_openai_service_tier: "" })).toBe("priority");
    expect(getGroupOpenAIServiceTier({ platform: "openai", force_openai_fast: true, force_openai_service_tier: "ultrafast" })).toBe("ultrafast");
  });

  it("keeps the legacy flag synchronized and clears unsupported platforms", () => {
    expect(groupOpenAIServiceTierToAPI("openai", "priority")).toEqual({ force_openai_service_tier: "priority", force_openai_fast: true, force_openai_ultrafast: false });
    expect(groupOpenAIServiceTierToAPI("composite", "ultrafast")).toEqual({ force_openai_service_tier: "ultrafast", force_openai_fast: false, force_openai_ultrafast: true });
    expect(groupOpenAIServiceTierToAPI("anthropic", "ultrafast")).toEqual({ force_openai_service_tier: "", force_openai_fast: false, force_openai_ultrafast: false });
  });

  it("round-trips official Ultra Fast groups and their Fast fallback without losing flags", () => {
    const officialGroup = { platform: "openai", force_openai_service_tier: "" as const, force_openai_fast: true, force_openai_ultrafast: true };
    const selected = getGroupOpenAIServiceTier(officialGroup);
    expect(selected).toBe("ultrafast_with_fast_fallback");
    expect(groupOpenAIServiceTierToAPI(officialGroup.platform, selected)).toEqual({ force_openai_fast: true, force_openai_ultrafast: true });
    expect(getGroupOpenAIServiceTier({ ...officialGroup, force_openai_fast: false })).toBe("supported_ultrafast");
    expect(groupOpenAIServiceTierToAPI("openai", "supported_ultrafast")).toEqual({ force_openai_fast: false, force_openai_ultrafast: true });
    expect(getGroupOpenAIServiceTier({ ...officialGroup, force_openai_service_tier: "priority" })).toBe("priority");
    expect(groupOpenAIServiceTierToAPI("openai", "")).toEqual({ force_openai_service_tier: "", force_openai_fast: false, force_openai_ultrafast: false });
    expect(groupOpenAIServiceTierToAPI("anthropic", selected)).toEqual({ force_openai_service_tier: "", force_openai_fast: false, force_openai_ultrafast: false });
  });

  it("provides localized Ultra Fast switch labels naming the supported models", () => {
    for (const locale of [zh, en]) {
      expect(locale.groups.openaiFast).toMatchObject({
        forceUltrafast: expect.stringContaining("ultrafast"),
        ultrafastHint: expect.stringContaining("Ultra Fast"),
      });
      expect(locale.groups.openaiFast.ultrafastHint).toContain("GPT-6 Astra");
      expect(locale.groups.openaiFast.ultrafastHint).toContain("GPT-6.1 Sol");
    }
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
