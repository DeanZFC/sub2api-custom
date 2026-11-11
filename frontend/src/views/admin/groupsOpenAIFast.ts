import type { GroupOpenAIServiceTier } from "@/types";

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
): GroupOpenAIServiceTier {
  if (!supportsGroupOpenAIFast(platform)) return "";
  return tier === "priority" || tier === "ultrafast" ? tier : "";
}

export function getGroupOpenAIServiceTier(group: {
  platform: string;
  force_openai_service_tier?: GroupOpenAIServiceTier;
  force_openai_fast?: boolean;
}): GroupOpenAIServiceTier {
  return normalizeGroupOpenAIServiceTier(
    group.platform,
    group.force_openai_service_tier ?? (group.force_openai_fast ? "priority" : ""),
  );
}

export function groupOpenAIServiceTierToAPI(
  platform: string,
  tier: GroupOpenAIServiceTier,
) {
  const normalized = normalizeGroupOpenAIServiceTier(platform, tier);
  return {
    force_openai_service_tier: normalized,
    force_openai_fast: normalized === "priority",
  };
}
