<template>
  <section class="mt-4 border-t border-gray-200 pt-4 dark:border-dark-400" data-testid="strict-failover">
    <div class="flex items-start justify-between gap-3">
      <div>
        <h4 class="text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('admin.groups.strictFailover.title') }}</h4>
        <p class="mt-1 text-xs text-gray-500">{{ t('admin.groups.strictFailover.hint') }}</p>
      </div>
      <Toggle :model-value="modelValue.enabled" :aria-label="t('admin.groups.strictFailover.enable')" data-testid="strict-toggle" @update:model-value="toggle" />
    </div>
    <div v-if="modelValue.enabled" class="mt-3 space-y-3">
      <p class="text-xs text-gray-500">{{ t('admin.groups.strictFailover.recovery') }}</p>
      <p v-if="loading" class="text-sm text-gray-500">{{ t('admin.groups.strictFailover.loading') }}</p>
      <div v-if="loadFailed" class="text-sm text-red-600" role="alert">
        {{ t('admin.groups.strictFailover.loadError') }}
        <button type="button" class="btn btn-secondary ml-2" @click="loadAccounts">{{ t('admin.groups.strictFailover.retry') }}</button>
      </div>
      <div v-for="(tier, index) in modelValue.tiers" :key="index" class="rounded-lg border border-gray-200 p-3 dark:border-dark-600" data-testid="strict-tier">
        <div class="mb-2 flex items-center justify-between gap-2">
          <label :for="'strict-tier-' + index" class="text-sm font-medium">{{ t('admin.groups.strictFailover.tier', { tier: index + 1 }) }}</label>
          <div class="flex gap-2">
            <button type="button" :disabled="index === 0" :aria-label="t('admin.groups.strictFailover.moveUp')" class="btn btn-secondary" @click="move(index, -1)">↑</button>
            <button type="button" :disabled="index === modelValue.tiers.length - 1" :aria-label="t('admin.groups.strictFailover.moveDown')" class="btn btn-secondary" @click="move(index, 1)">↓</button>
            <button type="button" class="text-sm text-red-600" @click="removeTier(index)">{{ t('admin.groups.strictFailover.remove') }}</button>
          </div>
        </div>
        <div class="mb-2 flex flex-wrap gap-2">
          <span v-for="id in tier" :key="id" class="rounded bg-primary-100 px-2 py-1 text-xs dark:bg-primary-900/30">
            {{ names.get(id) || '#' + id }}
            <button type="button" :aria-label="t('admin.groups.strictFailover.removeAccount') + ' ' + id" class="ml-1" @click="removeAccount(index, id)">×</button>
          </span>
        </div>
        <input :id="'strict-tier-' + index" v-model="search[index]" class="input" :placeholder="t('admin.groups.strictFailover.account')" @focus="activeTier = index" />
        <div v-if="activeTier === index" class="mt-1 max-h-40 overflow-auto rounded border border-gray-200 dark:border-dark-600">
          <button v-for="account in matches(index)" :key="account.id" type="button" class="block w-full px-3 py-2 text-left text-sm hover:bg-gray-100 dark:hover:bg-dark-700" @click="addAccount(index, account.id)">{{ account.name }} · #{{ account.id }}</button>
          <p v-if="!loading && matches(index).length === 0" class="px-3 py-2 text-xs text-gray-500">{{ t('admin.groups.strictFailover.noResults') }}</p>
        </div>
      </div>
      <button type="button" class="btn btn-secondary" :disabled="modelValue.tiers.length >= 10" data-testid="strict-add-tier" @click="addTier">{{ t('admin.groups.strictFailover.add') }}</button>
      <p v-if="invalid" class="text-sm text-red-600" role="alert">{{ t('admin.groups.strictFailover.empty') }}</p>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { StrictFailoverConfig } from '@/types'
import Toggle from '@/components/common/Toggle.vue'

const props = defineProps<{ groupId: number; modelValue: StrictFailoverConfig }>()
const emit = defineEmits<{ 'update:modelValue': [value: StrictFailoverConfig] }>()
const { t } = useI18n()
const accounts = ref<Array<{ id: number; name: string }>>([])
const names = computed(() => new Map(accounts.value.map(a => [a.id, a.name])))
const search = ref<string[]>([])
const activeTier = ref<number | null>(null)
const invalid = ref(false)
const loading = ref(false)
const loadFailed = ref(false)
let controller: AbortController | null = null
let loadedGroup: number | null = null
const publish = (tiers: number[][], enabled = props.modelValue.enabled) => {
  emit('update:modelValue', { enabled, tiers: tiers.map(tier => [...tier]) })
  invalid.value = false
}
const toggle = (enabled: boolean) => publish(enabled ? (props.modelValue.tiers.length ? props.modelValue.tiers : [[]]) : props.modelValue.tiers.filter(tier => tier.length > 0), enabled)
const addTier = () => publish([...props.modelValue.tiers, []])
const removeTier = (index: number) => { publish(props.modelValue.tiers.filter((_, i) => i !== index)); search.value = []; activeTier.value = null }
const move = (index: number, offset: number) => {
  const tiers = props.modelValue.tiers.map(tier => [...tier])
  const target = index + offset
  if (target < 0 || target >= tiers.length) return
  ;[tiers[index], tiers[target]] = [tiers[target]!, tiers[index]!]
  publish(tiers); search.value = []; activeTier.value = null
}
const addAccount = (index: number, id: number) => {
  if (props.modelValue.tiers.flat().includes(id)) return
  publish(props.modelValue.tiers.map((tier, i) => i === index ? [...tier, id] : tier))
  search.value[index] = ''; activeTier.value = null
}
const removeAccount = (index: number, id: number) => publish(props.modelValue.tiers.map((tier, i) => i === index ? tier.filter(value => value !== id) : tier))
const matches = (index: number) => {
  const assigned = new Set(props.modelValue.tiers.flat())
  const query = (search.value[index] || '').trim().toLowerCase()
  return accounts.value.filter(a => !assigned.has(a.id) && (!query || a.name.toLowerCase().includes(query) || String(a.id).includes(query))).slice(0, 50)
}
const loadAccounts = async () => {
  controller?.abort()
  const current = new AbortController(); controller = current
  loading.value = true; loadFailed.value = false
  const groupId = props.groupId
  try {
    const result: Array<{ id: number; name: string }> = []
    let page = 1
    while (true) {
      const response = await adminAPI.accounts.list(page, 100, { group: String(groupId) }, { signal: current.signal })
      if (current.signal.aborted) return
      result.push(...response.items.map(a => ({ id: a.id, name: a.name })))
      if (result.length >= response.total || response.items.length === 0) break
      page++
    }
    accounts.value = result; loadedGroup = groupId
  } catch {
    if (!current.signal.aborted) loadFailed.value = true
  } finally {
    if (controller === current) loading.value = false
  }
}
watch(() => [props.groupId, props.modelValue.enabled] as const, ([id, enabled]) => {
  if (loadedGroup !== id) { controller?.abort(); accounts.value = []; loadedGroup = null; activeTier.value = null; search.value = [] }
  if (enabled && loadedGroup !== id) void loadAccounts()
}, { immediate: true })
onBeforeUnmount(() => controller?.abort())
const validate = () => {
  const ids = props.modelValue.tiers.flat()
  invalid.value = props.modelValue.enabled && (props.modelValue.tiers.length === 0 || props.modelValue.tiers.length > 10 || props.modelValue.tiers.some(t => t.length === 0) || ids.length > 1000 || ids.some(id => !Number.isSafeInteger(id) || id <= 0) || new Set(ids).size !== ids.length)
  return !invalid.value
}
defineExpose({ validate })
</script>
