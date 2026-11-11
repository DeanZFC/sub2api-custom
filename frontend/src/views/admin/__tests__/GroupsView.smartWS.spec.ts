import { defineComponent, h } from "vue";
import { enableAutoUnmount, flushPromises, mount } from "@vue/test-utils";
import { createPinia } from "pinia";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

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
  getSmartWS,
  setSmartWS,
  showError,
  showSuccess,
  authState,
} = vi.hoisted(() => ({
  listGroups: vi.fn(),
  updateGroup: vi.fn(),
  createGroup: vi.fn(),
  getModelAllowlistCandidates: vi.fn(),
  getUsageSummary: vi.fn(),
  getCapacitySummary: vi.fn(),
  getLiveCapability: vi.fn(),
  getSmartWS: vi.fn(),
  setSmartWS: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
  authState: { isSimpleMode: false },
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

vi.mock("@/api/admin/groups", async () => ({
  ...await vi.importActual<typeof import("@/api/admin/groups")>("@/api/admin/groups"),
  getSmartWS,
  setSmartWS,
}));

vi.mock("@/stores/auth", () => ({ useAuthStore: () => authState }));

vi.mock("@/stores/app", () => ({
  useAppStore: () => ({
    showError,
    showSuccess,
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
  template: '<div data-testid="group-actions"><div v-if="data.length"><slot name="cell-actions" :row="data[0]" /></div></div>',
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

enableAutoUnmount(afterEach);

const submitEdit = async (wrapper: ReturnType<typeof mountView>) => {
  await wrapper.get("#edit-group-form").trigger("submit");
  await flushPromises();
};

describe("GroupsView SmartWS editing", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.resetAllMocks();
    authState.isSimpleMode = false;
    setGroups([sourceGroup]);
    getModelAllowlistCandidates.mockResolvedValue([]);
    getUsageSummary.mockResolvedValue([]);
    getCapacitySummary.mockResolvedValue([]);
    getLiveCapability.mockResolvedValue({ supported: false });
    createGroup.mockResolvedValue(sourceGroup);
    updateGroup.mockResolvedValue(sourceGroup);
    getSmartWS.mockResolvedValue({ enabled: false, revision: 4 });
    setSmartWS.mockResolvedValue({ enabled: true, revision: 5 });
  });

  it("moves the OpenAI setting into editing and retains simple-mode access", async () => {
    authState.isSimpleMode = true;
    const wrapper = mountView();
    await flushPromises();
    expect(wrapper.get('[data-testid="group-actions"]').text()).not.toContain("smartWS");
    expect(wrapper.find('[data-testid="group-smart-ws"]').exists()).toBe(false);
    expect(getSmartWS).not.toHaveBeenCalled();

    await openEdit(wrapper);

    expect(wrapper.get("#edit-group-form").find('[data-testid="group-smart-ws-settings"]').exists()).toBe(true);
    expect(getSmartWS).toHaveBeenCalledWith(42);
    expect((wrapper.get('[data-testid="edit-smart-ws-enabled"]').element as HTMLInputElement).checked).toBe(false);
    expect(setSmartWS).not.toHaveBeenCalled();
  });

  it("does not show or load SmartWS settings when editing a non-OpenAI group", async () => {
    setGroups([{ ...sourceGroup, name: "Anthropic", platform: "anthropic" }]);
    const wrapper = mountView();
    await flushPromises();

    await openEdit(wrapper);

    expect(wrapper.find("#edit-group-form").exists()).toBe(true);
    expect(wrapper.find('[data-testid="group-smart-ws-settings"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="edit-smart-ws-enabled"]').exists()).toBe(false);
    expect(getSmartWS).not.toHaveBeenCalled();
    expect(setSmartWS).not.toHaveBeenCalled();
  });

  it("updates other group fields without writing an unchanged SmartWS setting", async () => {
    getSmartWS.mockResolvedValue({ enabled: true, revision: 4 });
    const wrapper = mountView();
    await flushPromises();
    await openEdit(wrapper);
    await wrapper.get('input[data-tour="edit-group-form-name"]').setValue("Renamed group");

    await submitEdit(wrapper);

    expect(updateGroup).toHaveBeenCalledWith(42, expect.objectContaining({ name: "Renamed group" }));
    expect(setSmartWS).not.toHaveBeenCalled();
    expect(wrapper.find("#edit-group-form").exists()).toBe(false);
  });

  it("saves a changed SmartWS setting only after the group update succeeds", async () => {
    let finishGroupUpdate!: (group: AdminGroup) => void;
    updateGroup.mockImplementation(() => new Promise<AdminGroup>((resolve) => { finishGroupUpdate = resolve; }));
    const wrapper = mountView();
    await flushPromises();
    await openEdit(wrapper);
    await wrapper.get('[data-testid="edit-smart-ws-enabled"]').setValue(true);
    expect(setSmartWS).not.toHaveBeenCalled();

    await submitEdit(wrapper);

    expect(updateGroup).toHaveBeenCalledTimes(1);
    expect(setSmartWS).not.toHaveBeenCalled();
    expect(wrapper.find("#edit-group-form").exists()).toBe(true);
    finishGroupUpdate(sourceGroup);
    await flushPromises();
    expect(setSmartWS).toHaveBeenCalledWith(42, { enabled: true, revision: 4 });
    expect(wrapper.find("#edit-group-form").exists()).toBe(false);
    expect(showSuccess).toHaveBeenCalledWith("admin.groups.groupUpdated");
  });

  it("discards an unsaved SmartWS change when editing is cancelled", async () => {
    const wrapper = mountView();
    await flushPromises();
    await openEdit(wrapper);
    await wrapper.get('[data-testid="edit-smart-ws-enabled"]').setValue(true);

    await wrapper.findAll("button").find(button => button.text() === "common.cancel")!.trigger("click");
    await flushPromises();

    expect(updateGroup).not.toHaveBeenCalled();
    expect(setSmartWS).not.toHaveBeenCalled();
    expect(wrapper.find("#edit-group-form").exists()).toBe(false);
    await openEdit(wrapper);
    expect((wrapper.get('[data-testid="edit-smart-ws-enabled"]').element as HTMLInputElement).checked).toBe(false);
  });

  it("keeps editing open after a SmartWS conflict and retries the reviewed revision", async () => {
    const wrapper = mountView();
    await flushPromises();
    await openEdit(wrapper);
    await wrapper.get('[data-testid="edit-smart-ws-enabled"]').setValue(true);
    setSmartWS.mockRejectedValueOnce({ response: { status: 409 } });
    getSmartWS.mockResolvedValue({ enabled: true, revision: 9 });

    await submitEdit(wrapper);

    expect(updateGroup).toHaveBeenCalledTimes(1);
    expect(wrapper.find("#edit-group-form").exists()).toBe(true);
    expect(wrapper.get('[role="alert"]').text()).toContain("admin.groups.smartWS.conflict");
    expect(showError).toHaveBeenCalledWith("admin.groups.smartWS.groupSavePartial");
    expect(showSuccess).not.toHaveBeenCalled();
    expect(listGroups).toHaveBeenCalledTimes(2);
    expect((wrapper.get('[data-testid="edit-smart-ws-enabled"]').element as HTMLInputElement).checked).toBe(true);
    await wrapper.get('[data-testid="edit-smart-ws-enabled"]').setValue(false);
    setSmartWS.mockResolvedValue({ enabled: false, revision: 10 });
    await submitEdit(wrapper);
    expect(setSmartWS).toHaveBeenLastCalledWith(42, { enabled: false, revision: 9 });
    expect(wrapper.find("#edit-group-form").exists()).toBe(false);
  });

  it("does not replay completed account copying when retrying a failed SmartWS save", async () => {
    setGroups([sourceGroup, { ...sourceGroup, id: 43, name: "Copy source" }]);
    const wrapper = mountView();
    await flushPromises();
    await openEdit(wrapper);
    const copySourceSelect = wrapper.get("#edit-group-form").findAll("select")
      .find(select => select.find('option[value="43"]').exists())!;
    await copySourceSelect.setValue("43");
    await wrapper.get('[data-testid="edit-smart-ws-enabled"]').setValue(true);
    setSmartWS.mockRejectedValueOnce({ response: { status: 503 } });

    await submitEdit(wrapper);

    expect(updateGroup).toHaveBeenNthCalledWith(1, 42, expect.objectContaining({
      copy_accounts_from_group_ids: [43],
    }));
    expect(setSmartWS).toHaveBeenCalledTimes(1);
    expect(wrapper.find("#edit-group-form").exists()).toBe(true);
    expect(showError).toHaveBeenCalledWith("admin.groups.smartWS.groupSavePartial");
    await wrapper.get('[data-testid="edit-smart-ws-enabled"]').setValue(true);

    await submitEdit(wrapper);

    expect(updateGroup).toHaveBeenNthCalledWith(2, 42, expect.objectContaining({
      copy_accounts_from_group_ids: [],
    }));
    expect(setSmartWS).toHaveBeenCalledTimes(2);
    expect(wrapper.find("#edit-group-form").exists()).toBe(false);
  });

  it("retains the SmartWS draft without writing it when the group update fails", async () => {
    updateGroup.mockRejectedValue({ response: { data: { message: "Group update failed" } } });
    const wrapper = mountView();
    await flushPromises();
    await openEdit(wrapper);
    await wrapper.get('[data-testid="edit-smart-ws-enabled"]').setValue(true);

    await submitEdit(wrapper);

    expect(updateGroup).toHaveBeenCalledTimes(1);
    expect(setSmartWS).not.toHaveBeenCalled();
    expect(wrapper.find("#edit-group-form").exists()).toBe(true);
    expect((wrapper.get('[data-testid="edit-smart-ws-enabled"]').element as HTMLInputElement).checked).toBe(true);
    expect(showError).toHaveBeenCalled();
    expect(showSuccess).not.toHaveBeenCalled();
  });
});
