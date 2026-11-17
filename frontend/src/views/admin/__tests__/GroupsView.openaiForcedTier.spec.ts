import { defineComponent, h } from "vue";
import { flushPromises, mount } from "@vue/test-utils";
import { createPinia } from "pinia";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { AdminGroup } from "@/types";
import GroupsView from "@/views/admin/GroupsView.vue";

const {
  listGroups,
  getModelAllowlistCandidates,
  getUsageSummary,
  getCapacitySummary,
  getLiveCapability,
  createGroup,
  updateGroup,
} = vi.hoisted(() => ({
  listGroups: vi.fn(),
  getModelAllowlistCandidates: vi.fn(),
  getUsageSummary: vi.fn(),
  getCapacitySummary: vi.fn(),
  getLiveCapability: vi.fn(),
  createGroup: vi.fn(),
  updateGroup: vi.fn(),
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
  force_openai_ultrafast: false,
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

// 表单提交前会调用这些子组件的 validate()，自动桩不提供该方法。
const ValidatingFieldStub = defineComponent({
  setup(_, { expose }) {
    expose({ validate: () => true, resetValidation: () => undefined, save: async () => true });
    return () => h("div");
  },
});

const SelectStub = defineComponent({
  props: ["modelValue", "options"],
  emits: ["update:modelValue"],
  template: `<select :value="modelValue" @change="$emit('update:modelValue', $event.target.value)"><option v-for="option in options" :key="option.value" :value="option.value">{{ option.label }}</option></select>`,
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
        Select: SelectStub,
        PlatformIcon: true,
        Icon: true,
        GroupCapacityBadge: true,
        GroupRateMultipliersModal: true,
        GroupRPMOverridesModal: true,
        ReasoningEffortPolicyFields: ValidatingFieldStub,
        GroupSmartWSFields: ValidatingFieldStub,
        StrictFailoverFields: ValidatingFieldStub,
        CodexManifestAccountsField: ValidatingFieldStub,
        PricingEntryCard: true,
        VueDraggable: true,
      },
    },
  });

const openEditDialog = async (group: AdminGroup) => {
  listGroups.mockResolvedValue({
    items: [group],
    total: 1,
    page: 1,
    page_size: 20,
    pages: 1,
  });
  const wrapper = mountView();
  await flushPromises();
  const editButton = wrapper
    .findAll("button")
    .find((button) => button.text().includes("common.edit"));
  expect(editButton).toBeTruthy();
  await editButton!.trigger("click");
  await flushPromises();
  return wrapper;
};

const selectedTier = (wrapper: ReturnType<typeof mountView>) =>
  wrapper.get<HTMLSelectElement>('[data-testid="edit-force-openai-service-tier"]');

const submit = async (wrapper: ReturnType<typeof mountView>) => {
  await wrapper.get("#edit-group-form").trigger("submit");
  await flushPromises();
  expect(updateGroup).toHaveBeenCalledTimes(1);
  expect(updateGroup.mock.calls[0][0]).toBe(sourceGroup.id);
  return updateGroup.mock.calls[0][1];
};

describe("GroupsView OpenAI forced service tier", () => {
  beforeEach(() => {
    localStorage.clear();
    listGroups.mockReset();
    getModelAllowlistCandidates.mockReset();
    getUsageSummary.mockReset();
    getCapacitySummary.mockReset();
    getLiveCapability.mockReset();
    createGroup.mockReset();
    updateGroup.mockReset();

    getModelAllowlistCandidates.mockResolvedValue([]);
    getUsageSummary.mockResolvedValue([]);
    getCapacitySummary.mockResolvedValue([]);
    getLiveCapability.mockResolvedValue({ supported: false });
    updateGroup.mockResolvedValue({});
  });

  it("shows the client tier by default", async () => {
    const wrapper = await openEditDialog(sourceGroup);
    expect(selectedTier(wrapper).element.value).toBe("");
    wrapper.unmount();
  });

  it.each([
    [{ force_openai_fast: true, force_openai_ultrafast: false }, "priority"],
    [{ force_openai_fast: false, force_openai_ultrafast: true }, "supported_ultrafast"],
    [{ force_openai_fast: true, force_openai_ultrafast: true }, "ultrafast_with_fast_fallback"],
  ] as const)("loads and resaves the stored flags %o unchanged", async (flags, tier) => {
    const wrapper = await openEditDialog({ ...sourceGroup, force_openai_service_tier: "", ...flags });
    expect(selectedTier(wrapper).element.value).toBe(tier);
    const payload = await submit(wrapper);
    expect(payload).toMatchObject(flags);
    if (tier === "ultrafast_with_fast_fallback" || tier === "supported_ultrafast") {
      expect(payload).not.toHaveProperty("force_openai_service_tier");
    }
    wrapper.unmount();
  });

  it("can enable Ultra Fast while keeping Fast fallback for other models", async () => {
    const wrapper = await openEditDialog({ ...sourceGroup, force_openai_fast: true });
    await selectedTier(wrapper).setValue("ultrafast_with_fast_fallback");
    const payload = await submit(wrapper);
    expect(payload).toMatchObject({ force_openai_fast: true, force_openai_ultrafast: true });
    expect(payload).not.toHaveProperty("force_openai_service_tier");
    wrapper.unmount();
  });

  it("can remove Fast fallback while retaining Ultra Fast", async () => {
    const wrapper = await openEditDialog({ ...sourceGroup, force_openai_fast: true, force_openai_ultrafast: true });
    await selectedTier(wrapper).setValue("supported_ultrafast");
    const payload = await submit(wrapper);
    expect(payload).toMatchObject({ force_openai_fast: false, force_openai_ultrafast: true });
    expect(payload).not.toHaveProperty("force_openai_service_tier");
    wrapper.unmount();
  });

  it("preserves the custom all-model Ultrafast policy separately from the supported-model policy", async () => {
    const wrapper = await openEditDialog({ ...sourceGroup, force_openai_service_tier: "ultrafast", force_openai_ultrafast: true });
    expect(selectedTier(wrapper).element.value).toBe("ultrafast");
    expect(await submit(wrapper)).toMatchObject({ force_openai_service_tier: "ultrafast", force_openai_fast: false, force_openai_ultrafast: true });
    wrapper.unmount();
  });

  it("clears both flags when following the client again", async () => {
    const wrapper = await openEditDialog({ ...sourceGroup, force_openai_fast: true, force_openai_ultrafast: true });
    await selectedTier(wrapper).setValue("");
    expect(await submit(wrapper)).toMatchObject({ force_openai_service_tier: "", force_openai_fast: false, force_openai_ultrafast: false });
    wrapper.unmount();
  });
});
