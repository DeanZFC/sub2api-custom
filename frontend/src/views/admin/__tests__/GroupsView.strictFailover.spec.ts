import { defineComponent, h, type PropType } from "vue";
import { flushPromises, mount } from "@vue/test-utils";
import { createPinia } from "pinia";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { AdminGroup, StrictFailoverConfig } from "@/types";
import GroupUnavailableFallbackField from "@/components/admin/group/GroupUnavailableFallbackField.vue";
import GroupsView from "@/views/admin/GroupsView.vue";

const {
  listGroups,
  updateGroup,
  getModelAllowlistCandidates,
  getUsageSummary,
  getCapacitySummary,
  getLiveCapability,
} = vi.hoisted(() => ({
  listGroups: vi.fn(),
  updateGroup: vi.fn(),
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
      create: vi.fn(),
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

const StrictFailoverFieldsStub = defineComponent({
  name: "StrictFailoverFields",
  props: { modelValue: { type: Object as PropType<StrictFailoverConfig>, required: true } },
  emits: ["update:modelValue"],
  setup(props, { emit, expose }) {
    expose({ validate: () => true });
    return () => h("div", { "data-testid": "strict-field" }, [
      h("output", { "data-testid": "strict-value" }, JSON.stringify(props.modelValue)),
      h("button", { type: "button", "data-testid": "strict-configure", onClick: () => emit("update:modelValue", { enabled: true, tiers: [[11, 22], [33]] }) }, "configure"),
      h("button", { type: "button", "data-testid": "strict-disable", onClick: () => emit("update:modelValue", { ...props.modelValue, enabled: false }) }, "disable"),
    ]);
  },
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
        ReasoningEffortPolicyFields: defineComponent({ setup(_, { expose }) { expose({ validate: () => true, resetValidation: () => undefined }); return () => h("div"); } }),
        CodexManifestAccountsField: true,
        GroupUnavailableFallbackField: true,
        StrictFailoverFields: StrictFailoverFieldsStub,
        PricingEntryCard: true,
        VueDraggable: true,
      },
    },
  });

describe("GroupsView strict failover persistence", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.clearAllMocks();
    listGroups.mockResolvedValue({ items: [sourceGroup], total: 1, page: 1, page_size: 20, pages: 1 });
    getModelAllowlistCandidates.mockResolvedValue([]);
    getUsageSummary.mockResolvedValue([]);
    getCapacitySummary.mockResolvedValue([]);
    getLiveCapability.mockResolvedValue({ supported: false });
    updateGroup.mockResolvedValue(sourceGroup);
  });

  it("saves ordered tiers, reloads them, and can disable without losing the order", async () => {
    const wrapper = mountView();
    await flushPromises();
    const openEdit = async () => {
      await wrapper.findAll("button").find(button => button.text().includes("common.edit"))!.trigger("click");
      await flushPromises();
    };
    await openEdit();
    expect(JSON.parse(wrapper.get('[data-testid="strict-value"]').text())).toEqual({ enabled: false, tiers: [] });
    await wrapper.get('[data-testid="strict-configure"]').trigger("click");
    const configured = { enabled: true, tiers: [[11, 22], [33]] };
    listGroups.mockResolvedValue({ items: [{ ...sourceGroup, strict_failover: configured }], total: 1, page: 1, page_size: 20, pages: 1 });
    await wrapper.get("form").trigger("submit");
    await flushPromises();
    expect(updateGroup).toHaveBeenCalledWith(42, expect.objectContaining({ strict_failover: configured }));
    await openEdit();
    expect(JSON.parse(wrapper.get('[data-testid="strict-value"]').text())).toEqual(configured);
    await wrapper.get('[data-testid="strict-disable"]').trigger("click");
    await wrapper.get("form").trigger("submit");
    await flushPromises();
    expect(updateGroup).toHaveBeenLastCalledWith(42, expect.objectContaining({ strict_failover: { ...configured, enabled: false } }));
    wrapper.unmount();
  });
  it("saves the group fallback separately and sends zero to clear it", async () => {
    listGroups.mockResolvedValue({ items: [{ ...sourceGroup, fallback_group_id_on_unavailable: 88 }], total: 1, page: 1, page_size: 20, pages: 1 });
    const wrapper = mountView();
    await flushPromises();
    await wrapper.findAll("button").find(button => button.text().includes("common.edit"))!.trigger("click");
    await flushPromises();
    const field = wrapper.findComponent(GroupUnavailableFallbackField);
    expect(field.props("modelValue")).toBe(88);
    field.vm.$emit("update:modelValue", 89);
    await wrapper.get("form").trigger("submit");
    await flushPromises();
    expect(updateGroup).toHaveBeenLastCalledWith(42, expect.objectContaining({ fallback_group_id_on_unavailable: 89 }));
    await wrapper.findAll("button").find(button => button.text().includes("common.edit"))!.trigger("click");
    await flushPromises();
    wrapper.findComponent(GroupUnavailableFallbackField).vm.$emit("update:modelValue", null);
    await wrapper.get("form").trigger("submit");
    await flushPromises();
    expect(updateGroup).toHaveBeenLastCalledWith(42, expect.objectContaining({ fallback_group_id_on_unavailable: 0 }));
    wrapper.unmount();
  });

});
