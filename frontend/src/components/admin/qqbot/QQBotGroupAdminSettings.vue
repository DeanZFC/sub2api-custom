<template>
  <section class="card space-y-4 p-6">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <h2 class="text-lg font-semibold">{{ t('qqbot.groupAdmins.title') }}</h2>
      <button type="button" class="btn btn-secondary" data-testid="add-group-admin" :disabled="disabled || !appId || modelValue.length >= 100" @click="add()">{{ t('qqbot.groupAdmins.add') }}</button>
    </div>
    <p class="text-sm text-gray-500">{{ t('qqbot.groupAdmins.help') }}</p>
    <p v-if="!appId" class="text-sm text-amber-700">{{ t('qqbot.groupAdmins.appRequired') }}</p>
    <p v-if="!modelValue.length" class="text-sm text-gray-500">{{ t('qqbot.groupAdmins.empty') }}</p>
    <fieldset v-for="(admin, index) in modelValue" :key="index" :disabled="disabled" class="space-y-3 rounded-lg border p-4 dark:border-dark-700" data-testid="group-admin-entry">
      <legend class="px-1 text-sm font-medium">{{ t('qqbot.groupAdmins.entry', { index: index + 1 }) }}</legend>
      <div class="flex flex-wrap items-center justify-between gap-2">
        <span class="break-all text-xs text-gray-500">AppID: {{ admin.app_id }}</span>
        <button type="button" class="btn btn-secondary text-sm" :aria-label="t('qqbot.groupAdmins.remove', { index: index + 1 })" @click="remove(index)">{{ t('common.delete') }}</button>
      </div>
      <p v-if="admin.app_id !== appId" role="alert" class="text-sm text-amber-700">{{ t('qqbot.groupAdmins.appChanged') }}</p>
      <div class="grid gap-3 md:grid-cols-2">
        <label class="space-y-1">{{ t('qqbot.groupAdmins.group') }}<input :value="admin.group_id" list="qq-admin-groups" data-testid="group-admin-group" class="input font-mono" required maxlength="128" @input="update(index, 'group_id', $event)" /></label>
        <label class="space-y-1">{{ t('qqbot.groupAdmins.member') }}<input :value="admin.member_id" data-testid="group-admin-member" class="input font-mono" required maxlength="128" @input="update(index, 'member_id', $event)" /></label>
      </div>
      <label class="block space-y-1">{{ t('qqbot.groupAdmins.nickname') }}<input :value="admin.nickname" data-testid="group-admin-nickname" class="input" maxlength="100" @input="update(index, 'nickname', $event)" /></label>
    </fieldset>
    <datalist id="qq-admin-groups"><option v-for="group in groups" :key="group" :value="group" /></datalist>
    <div v-if="recentMembers.length" class="space-y-2 rounded-lg bg-gray-50 p-3 dark:bg-dark-800">
      <label class="block space-y-1 text-sm">{{ t('qqbot.groupAdmins.recent') }}
        <select class="input" data-testid="group-admin-recent" :disabled="disabled || !appId || modelValue.length >= 100" @change="addRecent">
          <option value="">{{ t('qqbot.groupAdmins.choose') }}</option>
          <option v-for="member in recentMembers" :key="`${member.group_id}:${member.member_id}`" :value="JSON.stringify(member)">{{ member.group_id }} / {{ member.member_id }}</option>
        </select>
      </label>
      <p class="text-xs text-gray-500">{{ t('qqbot.groupAdmins.recentHelp') }}</p>
    </div>
    <details class="rounded-lg border p-4 dark:border-dark-700">
      <summary class="cursor-pointer text-sm font-medium">{{ t('qqbot.groupAdmins.fromBindings') }}</summary>
      <div class="mt-3 flex flex-wrap gap-2">
        <input v-model="search" class="input min-w-0 flex-1" data-testid="group-admin-search" :aria-label="t('qqbot.groupAdmins.search')" :placeholder="t('qqbot.groupAdmins.search')" :disabled="disabled || loading" @keydown.enter.prevent="loadBindings(1)" />
        <button type="button" class="btn btn-secondary" data-testid="group-admin-search-button" :disabled="disabled || loading" @click="loadBindings(1)">{{ t('common.search') }}</button>
      </div>
      <p v-if="error" role="alert" class="mt-3 text-sm text-red-600">{{ error }}</p>
      <p v-if="bindings && !bindings.items.length" class="mt-3 text-sm text-gray-500">{{ t('qqbot.groupAdmins.noBindings') }}</p>
      <div v-for="binding in bindings?.items ?? []" :key="binding.member_id" class="mt-3 flex flex-wrap items-center justify-between gap-2 rounded-lg border p-3 dark:border-dark-700">
        <div class="min-w-0 text-sm"><p class="break-all">{{ binding.nickname || binding.email }}</p><p class="break-all text-xs text-gray-500">{{ binding.group_id }} / {{ binding.member_id }}</p></div>
        <button type="button" class="btn btn-secondary text-sm" :disabled="disabled || binding.app_id !== appId || !appId || modelValue.length >= 100 || isAdded(binding.group_id, binding.member_id)" @click="add({ app_id: binding.app_id, group_id: binding.group_id, member_id: binding.member_id, nickname: binding.nickname })">{{ t('qqbot.groupAdmins.select') }}</button>
      </div>
      <div v-if="bindings && bindings.total > bindings.page_size" class="mt-3 flex items-center justify-between gap-3">
        <button type="button" class="btn btn-secondary" :disabled="disabled || loading || bindings.page <= 1" @click="loadBindings(bindings.page - 1)">{{ t('pagination.previous') }}</button>
        <span class="text-sm">{{ bindings.page }} / {{ Math.ceil(bindings.total / bindings.page_size) }}</span>
        <button type="button" class="btn btn-secondary" :disabled="disabled || loading || bindings.page * bindings.page_size >= bindings.total" @click="loadBindings(bindings.page + 1)">{{ t('pagination.next') }}</button>
      </div>
    </details>
    <p class="text-xs text-gray-500">{{ t('qqbot.groupAdmins.saveHelp') }}</p>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { qqbotAPI, type QQAdminBindingsPage, type QQBotRuntime, type QQGroupAdmin } from '@/api/qqbot'

const props = defineProps<{ modelValue: QQGroupAdmin[]; appId: string; groups: string[]; runtime: QQBotRuntime | null; disabled: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: QQGroupAdmin[]] }>()
const { t } = useI18n()
const search = ref('')
const bindings = ref<QQAdminBindingsPage | null>(null)
const loading = ref(false)
const error = ref('')
const recentMembers = computed(() => {
  const members = new Map<string, { group_id: string; member_id: string }>()
  for (const job of props.runtime?.jobs ?? []) {
    if (!job.member_id || !props.groups.includes(job.group_id) || isAdded(job.group_id, job.member_id)) continue
    const key = JSON.stringify([job.group_id, job.member_id])
    members.set(key, { group_id: job.group_id, member_id: job.member_id })
  }
  return [...members.values()]
})
function isAdded(group: string, member: string) {
  return props.modelValue.some(admin => admin.app_id === props.appId && admin.group_id === group && admin.member_id === member)
}
function add(admin?: QQGroupAdmin) {
  if (props.disabled || !props.appId || props.modelValue.length >= 100) return
  if (admin && isAdded(admin.group_id, admin.member_id)) return
  emit('update:modelValue', [...props.modelValue, admin ?? { app_id: props.appId, group_id: props.groups[0] ?? '', member_id: '', nickname: '' }])
}
function remove(index: number) { emit('update:modelValue', props.modelValue.filter((_, i) => i !== index)) }
function update(index: number, field: 'group_id' | 'member_id' | 'nickname', event: Event) {
  const value = (event.target as HTMLInputElement).value
  emit('update:modelValue', props.modelValue.map((admin, i) => i === index ? { ...admin, [field]: value } : admin))
}
function addRecent(event: Event) {
  const select = event.target as HTMLSelectElement
  const member = recentMembers.value.find(item => JSON.stringify(item) === select.value)
  if (member) add({ ...member, app_id: props.appId, nickname: '' })
  select.value = ''
}
async function loadBindings(page: number) {
  if (loading.value || props.disabled) return
  loading.value = true; error.value = ''
  try { bindings.value = await qqbotAPI.bindings(search.value.trim(), page) }
  catch (e) { error.value = (e as { message?: string })?.message || t('common.error') }
  finally { loading.value = false }
}
</script>
