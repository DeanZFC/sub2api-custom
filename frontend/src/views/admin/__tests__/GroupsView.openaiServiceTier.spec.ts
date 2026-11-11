import { defineComponent, h } from "vue";
import { flushPromises, mount } from "@vue/test-utils";
import { createPinia } from "pinia";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { AdminGroup } from "@/types";
import GroupsView from "@/views/admin/GroupsView.vue";

const {
  listGroups,
  updateGroup,
  createGroup,
  getModelAllowlistCandidates,
  getUsageSummary,
  getCapacitySummary,
  getLiveCapability,
} = vi.hoisted(() => ({
  listGroups: vi.fn(),
  updateGroup: vi.fn(),
  createGroup: vi.fn(),
  getModelAllowlistCandidates: vi.fn(),
  getUsageSummary: vi.fn(),
  getCapacitySummary: vi.fn(),
  getLiveCapability: vi.fn(),
}));

vi.mock("@/api/admin", () => ({
  adminAPI: {
    groups: {
      list: listGroups,
      getAll: vi.fn(),
      getModelAllowlistCandidates,
      getUsageSummary,
      getCapacitySummary,
      getLiveCapability,
      create: createGroup,
      update: updateGroup,
      delete: vi.fn(),
      duplicate: vi.fn(),
      updateSortOrder: vi.fn(),
    },
    accounts: {
      list: vi.fn(),
      getById: vi.fn(),
    },
  },
}));

vi.mock("@/stores/app", () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn(),
  }),
}));

vi.mock("@/stores/onboarding", () => ({
  useOnboardingStore: () => ({
    isCurrentStep: vi.fn(() => false),
    nextStep: vi.fn(),
  }),
}));

vi.mock("vue-i18n", async () => {
  const actual = await vi.importActual<typeof import("vue-i18n")>("vue-i18n");
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  };
});

const sourceGroup = {
  id: 42,
  name: "OpenAI",
  description: null,
  platform: "openai",
  rate_multiplier: 1,
  rpm_limit: 0,
  is_exclusive: false,
  status: "active",
  subscription_type: "standard",
  daily_limit_usd: null,
  weekly_limit_usd: null,
  monthly_limit_usd: null,
  long_context_pricing_enabled: true,
  force_openai_fast: false,
  free_openai_fast: false,
  model_pricing: [],
  profit_control_enabled: false,
  profit_min_margin: 0,
  profit_safety_buffer: 0,
  allow_image_generation: false,
  allow_batch_image_generation: false,
  image_rate_independent: false,
  image_rate_multiplier: 1,
  batch_image_discount_multiplier: 0.5,
  batch_image_hold_multiplier: 0.6,
  image_price_1k: null,
  image_price_2k: null,
  image_price_4k: null,
  video_rate_independent: false,
  video_rate_multiplier: 1,
  video_price_480p: null,
  video_price_720p: null,
  video_price_1080p: null,
  web_search_price_per_call: null,
  search_price_per_1k: null,
  audio_realtime_price_per_min: null,
  audio_tts_price_per_million_chars: null,
  audio_stt_price_per_hour: null,
  peak_rate_enabled: false,
  peak_start: "",
  peak_end: "",
  peak_rate_multiplier: 1,
  claude_code_only: false,
  fallback_group_id: null,
  fallback_group_id_on_invalid_request: null,
  allow_messages_dispatch: false,
  allow_live: false,
  require_oauth_only: false,
  require_privacy_set: false,
  created_at: "2026-09-05T00:00:00Z",
  updated_at: "2026-09-05T00:00:00Z",
  model_routing: null,
  model_routing_enabled: false,
  mcp_xml_inject: true,
  supported_model_scopes: [],
  account_count: 1,
  active_account_count: 1,
  rate_limited_account_count: 0,
  models_list_config: undefined,
  codex_models_manifest_config: {
    enabled: false,
    account_ids: [],
    fallback_to_scheduler: false,
  },
  sort_order: 10,
} satisfies AdminGroup;

const AppLayoutStub = defineComponent({
  template: "<main><slot /></main>",
});

const TablePageLayoutStub = defineComponent({
  template: '<section><slot name="filters" /><slot name="table" /><slot name="pagination" /></section>',
});

const DataTableStub = defineComponent({
  props: {
    data: { type: Array, default: () => [] },
  },
  template: '<div><div v-if="data.length"><slot name="cell-actions" :row="data[0]" /></div></div>',
});

const BaseDialogStub = defineComponent({
  props: {
    show: { type: Boolean, default: false },
  },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>',
});

const mountView = () =>
  mount(GroupsView, {
    global: {
      plugins: [createPinia()],
      stubs: {
        AppLayout: AppLayoutStub,
        TablePageLayout: TablePageLayoutStub,
        DataTable: DataTableStub,
        Pagination: true,
        BaseDialog: BaseDialogStub,
        ConfirmDialog: true,
        EmptyState: true,
        Select: true,
        PlatformIcon: true,
        Icon: true,
        GroupCapacityBadge: true,
        GroupRateMultipliersModal: true,
        GroupRPMOverridesModal: true,
        GroupSmartWSFields: defineComponent({ setup(_, { expose }) { expose({ loading: false, validate: () => true, save: async () => true }); return () => h("div"); } }),
        ReasoningEffortPolicyFields: defineComponent({ setup(_, { expose }) { expose({ validate: () => true, resetValidation: () => undefined }); return () => h("div"); } }),
        CodexManifestAccountsField: true,
        GroupUnavailableFallbackField: true,
        StrictFailoverFields: defineComponent({ setup(_, { expose }) { expose({ validate: () => true }); return () => h("div"); } }),
        PricingEntryCard: true,
        VueDraggable: true,
      },
    },
  });


const setGroups = (groups: AdminGroup[]) => {
  listGroups.mockResolvedValue({ items: groups, total: groups.length, page: 1, page_size: 20, pages: 1 });
};

const openEdit = async (wrapper: ReturnType<typeof mountView>) => {
  await wrapper.findAll("button").find(button => button.text().includes("common.edit"))!.trigger("click");
  await flushPromises();
};

const selectTier = async (wrapper: ReturnType<typeof mountView>, form: "create" | "edit", tier: string) => {
  wrapper.getComponent(`[data-testid="${form}-force-openai-service-tier"]`).vm.$emit("update:modelValue", tier);
  await flushPromises();
};

describe("GroupsView OpenAI service tier", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.clearAllMocks();
    setGroups([sourceGroup]);
    getModelAllowlistCandidates.mockResolvedValue([]);
    getUsageSummary.mockResolvedValue([]);
    getCapacitySummary.mockResolvedValue([]);
    getLiveCapability.mockResolvedValue({ supported: false });
    createGroup.mockResolvedValue(sourceGroup);
    updateGroup.mockResolvedValue(sourceGroup);
  });

  it("creates an Ultrafast group and resets the selection for the next group", async () => {
    const wrapper = mountView();
    await flushPromises();
    await wrapper.get('[data-tour="groups-create-btn"]').trigger("click");
    wrapper.getComponent('[data-tour="group-form-platform"]').vm.$emit("update:modelValue", "openai");
    await flushPromises();
    await wrapper.get('input[data-tour="group-form-name"]').setValue("Ultrafast group");
    await selectTier(wrapper, "create", "ultrafast");
    await wrapper.get("form").trigger("submit");
    await flushPromises();
    expect(createGroup).toHaveBeenCalledWith(expect.objectContaining({
      force_openai_service_tier: "ultrafast",
      force_openai_fast: false,
      free_openai_fast: false,
    }));
    await wrapper.get('[data-tour="groups-create-btn"]').trigger("click");
    wrapper.getComponent('[data-tour="group-form-platform"]').vm.$emit("update:modelValue", "openai");
    await flushPromises();
    expect(wrapper.getComponent('[data-testid="create-force-openai-service-tier"]').props("modelValue")).toBe("");
    wrapper.unmount();
  });

  it("loads legacy Fast, persists Ultrafast, reloads it, and explicitly disables forcing", async () => {
    setGroups([{ ...sourceGroup, force_openai_fast: true }]);
    const wrapper = mountView();
    await flushPromises();
    await openEdit(wrapper);
    expect(wrapper.getComponent('[data-testid="edit-force-openai-service-tier"]').props("modelValue")).toBe("priority");
    await selectTier(wrapper, "edit", "ultrafast");
    setGroups([{ ...sourceGroup, force_openai_service_tier: "ultrafast" }]);
    await wrapper.get("form").trigger("submit");
    await flushPromises();
    expect(updateGroup).toHaveBeenLastCalledWith(42, expect.objectContaining({ force_openai_service_tier: "ultrafast", force_openai_fast: false }));
    await openEdit(wrapper);
    expect(wrapper.getComponent('[data-testid="edit-force-openai-service-tier"]').props("modelValue")).toBe("ultrafast");
    await selectTier(wrapper, "edit", "");
    await wrapper.get("form").trigger("submit");
    await flushPromises();
    expect(updateGroup).toHaveBeenLastCalledWith(42, expect.objectContaining({ force_openai_service_tier: "", force_openai_fast: false }));
    wrapper.unmount();
  });

  it("clears a selected tier after switching to an unsupported platform", async () => {
    const wrapper = mountView();
    await flushPromises();
    await wrapper.get('[data-tour="groups-create-btn"]').trigger("click");
    wrapper.getComponent('[data-tour="group-form-platform"]').vm.$emit("update:modelValue", "openai");
    await flushPromises();
    await selectTier(wrapper, "create", "ultrafast");
    wrapper.getComponent('[data-tour="group-form-platform"]').vm.$emit("update:modelValue", "anthropic");
    await flushPromises();
    expect(wrapper.find('[data-testid="create-force-openai-service-tier"]').exists()).toBe(false);
    await wrapper.get('input[data-tour="group-form-name"]').setValue("Anthropic group");
    await wrapper.get("form").trigger("submit");
    await flushPromises();
    expect(createGroup).toHaveBeenLastCalledWith(expect.objectContaining({ platform: "anthropic", force_openai_service_tier: "", force_openai_fast: false }));
    wrapper.unmount();
  });
});
