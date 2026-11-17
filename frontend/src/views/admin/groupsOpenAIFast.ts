import type { GroupOpenAIServiceTier } from "@/types";

export type GroupOpenAIServiceTierSelection = GroupOpenAIServiceTier | "supported_ultrafast" | "ultrafast_with_fast_fallback";

export function supportsGroupOpenAIFast(platform: string): boolean {
  return platform === "openai" || platform === "composite";
}

export function normalizeGroupOpenAIFast(
  platform: string,
  enabled: boolean,
): boolean {
  return supportsGroupOpenAIFast(platform) && enabled;
}

export function normalizeGroupOpenAIServiceTier(
  platform: string,
  tier: string | null | undefined,
): GroupOpenAIServiceTierSelection {
  if (!supportsGroupOpenAIFast(platform)) return "";
  return tier === "priority" || tier === "ultrafast" || tier === "supported_ultrafast" || tier === "ultrafast_with_fast_fallback" ? tier : "";
}

export function getGroupOpenAIServiceTier(group: {
  platform: string;
  force_openai_service_tier?: GroupOpenAIServiceTier;
  force_openai_fast?: boolean;
  force_openai_ultrafast?: boolean;
}): GroupOpenAIServiceTierSelection {
  return normalizeGroupOpenAIServiceTier(
    group.platform,
    group.force_openai_service_tier ||
      (group.force_openai_ultrafast
        ? (group.force_openai_fast ? "ultrafast_with_fast_fallback" : "supported_ultrafast")
        : (group.force_openai_fast ? "priority" : "")),
  );
}

export function groupOpenAIServiceTierToAPI(
  platform: string,
  tier: GroupOpenAIServiceTierSelection,
) {
  const normalized = normalizeGroupOpenAIServiceTier(platform, tier);
  if (normalized === "supported_ultrafast" || normalized === "ultrafast_with_fast_fallback") {
    return { force_openai_fast: normalized === "ultrafast_with_fast_fallback", force_openai_ultrafast: true };
  }
  return {
    force_openai_service_tier: normalized,
    force_openai_fast: normalized === "priority",
    force_openai_ultrafast: normalized === "ultrafast",
  };
}
