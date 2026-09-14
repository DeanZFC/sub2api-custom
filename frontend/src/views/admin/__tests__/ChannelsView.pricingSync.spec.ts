import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import ChannelsView from '@/views/admin/ChannelsView.vue'
import type { Channel } from '@/api/admin/channels'

const {
  listChannels,
  getAllGroups,
  getWebSearchConfig,
  syncPricingModels,
  getModelDefaultPricing,
  updateChannel,
  showSuccess,
  showError,
} = vi.hoisted(() => ({
  listChannels: vi.fn(),
  getAllGroups: vi.fn(),
  getWebSearchConfig: vi.fn(),
  syncPricingModels: vi.fn(),
  getModelDefaultPricing: vi.fn(),
  updateChannel: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn(),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    channels: {
      list: listChannels,
      getModelDefaultPricing,
      syncPricingModels,
      update: updateChannel,
      create: vi.fn(),
      remove: vi.fn(),
    },
    groups: { getAll: getAllGroups },
    settings: { getWebSearchEmulationConfig: getWebSearchConfig },
    accounts: { list: vi.fn(), getById: vi.fn() },
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showSuccess, showError }),
}))

vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string, _params?: unknown, fallback?: string) => fallback || key }),
}))

const AppLayoutStub = defineComponent({ template: '<main><slot /></main>' })
const TablePageLayoutStub = defineComponent({
  template: '<section><slot name="filters" /><slot name="table" /><slot name="pagination" /></section>',
})
const DataTableStub = defineComponent({
  props: { data: { type: Array, default: () => [] } },
  template: '<div><div v-for="row in data" :key="row.id"><slot name="cell-actions" :row="row" /></div></div>',
})
const BaseDialogStub = defineComponent({
  props: { show: Boolean },
  template: '<div v-if="show" data-test="dialog"><slot /><slot name="footer" /></div>',
})
const PricingEntryCardStub = defineComponent({
  props: { entry: { type: Object, required: true } },
  template: '<div class="pricing-entry" :data-models="entry.models.join()" :data-input="String(entry.input_price)" />',
})

const SelectStub = defineComponent({
  props: { modelValue: { type: [String, Number, Boolean], default: '' }, options: { type: Array, default: () => [] } },
  emits: ['update:modelValue', 'change'],
  template: '<select :value="modelValue" @change="$emit(\'update:modelValue\', $event.target.value); $emit(\'change\', $event.target.value)"><option v-for="o in options" :key="String(o.value)" :value="o.value">{{ o.label }}</option></select>',
})

const ToggleStub = defineComponent({
  props: { modelValue: Boolean },
  emits: ['update:modelValue'],
  template: '<button type="button" @click="$emit(\'update:modelValue\', !modelValue)">toggle</button>',
})

function makeGroup() {
  return {
    id: 11,
    name: 'DeepSeek shared',
    description: null,
    platform: 'deepseek',
    rate_multiplier: 1,
    is_exclusive: false,
    status: 'active',
    subscription_type: 'standard',
    daily_limit_usd: null,
    weekly_limit_usd: null,
    monthly_limit_usd: null,
    long_context_pricing_enabled: false,
    allow_image_generation: false,
    allow_batch_image_generation: false,
    image_rate_independent: false,
    image_rate_multiplier: 1,
    batch_image_discount_multiplier: 1,
    batch_image_hold_multiplier: 1,
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
    peak_start: '',
    peak_end: '',
    peak_rate_multiplier: 1,
    claude_code_only: false,
    fallback_group_id: null,
    fallback_group_id_on_invalid_request: null,
    allow_live: false,
    require_oauth_only: false,
    require_privacy_set: false,
    created_at: '',
    updated_at: '',
    force_openai_fast: false,
    free_openai_fast: false,
    model_pricing: [],
    profit_control_enabled: false,
    profit_min_margin: 0,
    profit_safety_buffer: 0,
    model_routing: null,
    model_routing_enabled: false,
    mcp_xml_inject: false,
    account_count: 1,
    sort_order: 0,
  }
}

const models = [
  'deepseek-chat', 'deepseek-reasoner', 'deepseek-v3', 'deepseek-v3.1',
  'deepseek-v4', 'deepseek-flash', 'deepseek-pro',
]

function makeChannel(): Channel {
  return {
    id: 7,
    name: 'deepseek-channel',
    description: '',
    status: 'active',
    billing_model_source: 'channel_mapped',
    restrict_models: false,
    group_ids: [11],
    model_mapping: {},
    model_pricing: [{
      platform: 'deepseek', models,
      billing_mode: 'token',
      input_price: 0.000001,
      output_price: 0.000002,
      cache_write_price: null,
      cache_write_1h_price: null,
      cache_read_price: null,
      image_input_price: null,
      image_output_price: null,
      per_request_price: null,
      intervals: [],
      time_pricing: null,
    }],
    features_config: {},
    apply_pricing_to_account_stats: false,
    account_stats_pricing_rules: [],
    created_at: '',
    updated_at: '',
  }
}

function mountView() {
  return mount(ChannelsView, {
    global: {
      stubs: {
        AppLayout: AppLayoutStub,
        TablePageLayout: TablePageLayoutStub,
        DataTable: DataTableStub,
        BaseDialog: BaseDialogStub,
        PricingEntryCard: PricingEntryCardStub,
        Select: SelectStub,
        Toggle: ToggleStub,
        Pagination: true,
        ConfirmDialog: true,
        EmptyState: true,
        Icon: true,
        PlatformIcon: true,
      },
    },
  })
}

describe('ChannelsView DeepSeek pricing synchronization', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    listChannels.mockResolvedValue({ items: [makeChannel()], total: 1 })
    getAllGroups.mockResolvedValue([makeGroup()])
    getWebSearchConfig.mockResolvedValue({ enabled: false, providers: [] })
    syncPricingModels.mockResolvedValue({ models })
    getModelDefaultPricing.mockImplementation(async (model: string) => ({
      found: true,
      input_price: model.includes('flash') ? 0.000003 : 0.000001,
      output_price: model.includes('flash') ? 0.000004 : 0.000002,
      cache_write_price: null,
      cache_read_price: null,
      image_input_price: null,
      image_output_price: null,
    }))
    updateChannel.mockResolvedValue(makeChannel())
  })

  async function openEditor() {
    const wrapper = mountView()
    await flushPromises()
    const edit = wrapper.findAll('button').find(button => button.text().includes('common.edit'))
    expect(edit).toBeTruthy()
    await edit!.trigger('click')
    await flushPromises()
    return wrapper
  }

  it('does not fetch default prices while opening an editor', async () => {
    const wrapper = await openEditor()
    expect(getModelDefaultPricing).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('splits the shared DeepSeek pricing entry into two default-price entries', async () => {
    const wrapper = await openEditor()
    const deepseekTab = wrapper.findAll('button').find(button => button.text().includes('admin.groups.platforms.deepseek'))
    expect(deepseekTab).toBeTruthy()
    await deepseekTab!.trigger('click')
    const split = wrapper.findAll('button').find(button => button.text().includes('admin.channels.form.splitDefaultPrices'))
    expect(split).toBeTruthy()
    await split!.trigger('click')
    await flushPromises()
    expect(wrapper.findAll('.pricing-entry')).toHaveLength(2)
    expect(wrapper.findAll('.pricing-entry').map(node => node.attributes('data-models'))).toEqual([
      'deepseek-chat,deepseek-reasoner,deepseek-v3,deepseek-v3.1,deepseek-v4,deepseek-pro',
      'deepseek-flash',
    ])
    wrapper.unmount()
  })

  it('saves split prices as per-token values and does not overwrite a manual price on no-op sync', async () => {
    const wrapper = await openEditor()
    const deepseekTab = wrapper.findAll('button').find(button => button.text().includes('admin.groups.platforms.deepseek'))!
    await deepseekTab.trigger('click')
    const sync = wrapper.findAll('button').find(button => button.text().includes('admin.channels.form.syncLatestModels'))!
    await sync.trigger('click')
    await flushPromises()
    expect(getModelDefaultPricing).not.toHaveBeenCalled()
    expect(wrapper.findAll('.pricing-entry')).toHaveLength(1)
    const split = wrapper.findAll('button').find(button => button.text().includes('admin.channels.form.splitDefaultPrices'))!
    await split.trigger('click')
    await flushPromises()
    await wrapper.find('form#channel-form').trigger('submit')
    await flushPromises()
    expect(updateChannel).toHaveBeenCalledTimes(1)
    const request = updateChannel.mock.calls[0][1]
    expect(request.model_pricing).toEqual(expect.arrayContaining([
      expect.objectContaining({ models: ['deepseek-flash'], input_price: 0.000003, output_price: 0.000004 }),
      expect.objectContaining({ models: expect.arrayContaining(['deepseek-chat']), input_price: 0.000001, output_price: 0.000002 }),
    ]))
    wrapper.unmount()
  })

  it('leaves prices unchanged when default-price lookup fails', async () => {
    getModelDefaultPricing.mockRejectedValue(new Error('catalog unavailable'))
    const wrapper = await openEditor()
    const deepseekTab = wrapper.findAll('button').find(button => button.text().includes('admin.groups.platforms.deepseek'))!
    await deepseekTab.trigger('click')
    const split = wrapper.findAll('button').find(button => button.text().includes('admin.channels.form.splitDefaultPrices'))!
    await split.trigger('click')
    await flushPromises()
    expect(wrapper.findAll('.pricing-entry')).toHaveLength(1)
    expect(showError).toHaveBeenCalled()
    wrapper.unmount()
  })
})
