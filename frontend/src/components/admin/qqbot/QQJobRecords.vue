<template>
  <section class="card space-y-4 p-5">
    <form class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4" @submit.prevent="search">
      <label class="text-sm">{{ t('qqbot.group') }}<input v-model.trim="filters.group_id" class="input" maxlength="128" /></label>
      <label class="text-sm">{{ t('qqbot.workspace.memberID') }}<input v-model.trim="filters.member_id" class="input" maxlength="128" /></label>
      <label class="text-sm">{{ t('qqbot.workspace.jobKind') }}<select v-model="filters.kind" class="input"><option value="">{{ t('qqbot.workspace.all') }}</option><option v-for="kind in kinds" :key="kind" :value="kind">{{ kind }}</option></select></label>
      <label class="text-sm">{{ t('common.status') }}<select v-model="filters.status" class="input"><option value="">{{ t('qqbot.workspace.all') }}</option><option v-for="status in statuses" :key="status" :value="status">{{ status }}</option></select></label>
      <label class="text-sm">{{ t('qqbot.workspace.from') }}<input v-model="filters.from" class="input" type="datetime-local" /></label>
      <label class="text-sm">{{ t('qqbot.workspace.to') }}<input v-model="filters.to" class="input" type="datetime-local" /></label>
      <button class="btn btn-primary self-end" :disabled="loading">{{ t('common.search') }}</button>
    </form>
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    <p class="text-xs text-gray-500">{{ t('qqbot.workspace.recordsHelp') }}</p>
    <article v-for="job in data.items" :key="job.id" class="space-y-2 rounded-lg border p-4 text-sm dark:border-dark-700">
      <p class="flex flex-wrap justify-between gap-2"><strong>{{ job.kind || 'event' }} · {{ job.status }}</strong><time>{{ new Date(job.created_at).toLocaleString() }}</time></p>
      <p class="break-all text-xs text-gray-500">{{ job.group_id }} · {{ job.member_id }} · {{ job.id }}</p>
      <p v-if="job.trigger_reason || job.decision_reason">{{ job.trigger_reason }} → {{ job.decision_reason || job.status }}</p>
      <div v-if="job.image_decision?.tool" class="rounded bg-gray-50 p-3 text-xs dark:bg-dark-800">
        <p>{{ t('qqbot.workspace.imageDecision') }}: {{ job.image_decision.tool }} → {{ job.image_decision.review }} → {{ job.image_decision.execution }}</p>
        <p>{{ t('qqbot.workspace.imageReason') }}: {{ job.image_decision.reason }} · {{ job.image_decision.basis || 'none' }}</p>
        <p>{{ t('qqbot.workspace.imageMessageRef') }}: {{ job.image_decision.message_ref }}<span v-if="job.image_decision.plan_ref"> · {{ t('qqbot.workspace.imagePlanRef') }}: {{ job.image_decision.plan_ref }}</span></p>
      </div>
      <p>Tokens: {{ job.input_tokens }} / {{ job.output_tokens }}</p>
      <p v-if="job.error" class="break-all text-red-600">{{ job.error }}</p>
    </article>
    <p v-if="!loading && !data.items.length">{{ t('common.noData') }}</p>
    <div class="flex flex-wrap items-center justify-between gap-3 text-sm"><span>{{ t('qqbot.recordsPage', { total: data.total, page: data.page, pages }) }}</span><div class="flex gap-2"><button class="btn btn-secondary" :disabled="loading || data.page <= 1" @click="load(data.page - 1)">{{ t('qqbot.previousPage') }}</button><button class="btn btn-secondary" :disabled="loading || data.page >= pages" @click="load(data.page + 1)">{{ t('qqbot.nextPage') }}</button></div></div>
  </section>
</template>
<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { qqbotAPI, type QQJobPage } from '@/api/qqbot'
const props = defineProps<{ notifications?: boolean }>()
const { t } = useI18n()
const filters = reactive({ group_id: '', member_id: '', kind: props.notifications ? 'notifications' : '', status: '', from: '', to: '' })
let applied = { ...filters }
function search() { applied = { ...filters }; void load(1) }
const kinds = ['chat', 'image', 'ambient_chat', 'notifications', 'price_notification', 'health_notification', 'ranking_notification']
const statuses = ['queued', 'running', 'completed', 'skipped', 'failed', 'delivery_failed', 'expired', 'interrupted', 'cancelled']
const loading = ref(false), error = ref('')
const data = ref<QQJobPage>({ items: [], total: 0, page: 1, page_size: 20 })
const pages = computed(() => Math.max(1, Math.ceil(data.value.total / data.value.page_size)))
let request = 0
async function load(page: number) {
 const seq = ++request; loading.value = true; error.value = ''
 try {
  const result = await qqbotAPI.jobs({ ...applied, from: applied.from ? new Date(applied.from).toISOString() : '', to: applied.to ? new Date(applied.to).toISOString() : '', page, page_size: 20 })
  if (seq === request) data.value = result
 } catch (e) { if (seq === request) error.value = (e as Error).message } finally { if (seq === request) loading.value = false }
}
watch(() => props.notifications, value => { filters.kind = value ? 'notifications' : ''; search() }, { immediate: true })
</script>
