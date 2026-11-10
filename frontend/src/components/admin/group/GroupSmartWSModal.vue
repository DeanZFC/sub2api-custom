<template>
  <BaseDialog :show="show" :title="t('admin.groups.smartWS.title')" @close="close">
    <div class="space-y-4">
      <p class="font-medium text-gray-900 dark:text-white">{{ group?.name }}</p>
      <p class="text-sm text-gray-600 dark:text-gray-400">{{ t('admin.groups.smartWS.description') }}</p>
      <p v-if="loading" role="status">{{ t('common.loading') }}</p>
      <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
      <label class="flex items-center gap-3 text-sm">
        <input v-model="enabled" type="checkbox" :disabled="loading || saving || !policy" />
        {{ t('admin.groups.smartWS.enable') }}
      </label>
      <p class="text-sm text-gray-500">{{ t('admin.groups.smartWS.hint') }}</p>
    </div>
    <template #footer>
      <button class="btn btn-secondary" :disabled="saving" @click="close">{{ t('common.cancel') }}</button>
      <button class="btn btn-primary" :disabled="loading || saving || !policy" @click="save">{{ t('common.save') }}</button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { getSmartWS, setSmartWS, type SmartWSPolicy } from '@/api/admin/groups'
import type { AdminGroup } from '@/types'

const props = defineProps<{ show: boolean; group: AdminGroup | null }>()
const emit = defineEmits<{ close: [] }>()
const { t } = useI18n()
const policy = ref<SmartWSPolicy | null>(null)
const enabled = ref(false)
const loading = ref(false)
const saving = ref(false)
const error = ref('')
let generation = 0

watch(() => [props.show, props.group?.id] as const, async ([show, id]) => {
  const current = ++generation
  policy.value = null
  enabled.value = false
  error.value = ''
  loading.value = false
  if (!show || !id) return
  loading.value = true
  try {
    const result = await getSmartWS(id)
    if (current !== generation) return
    policy.value = result
    enabled.value = result.enabled
  } catch {
    if (current === generation) error.value = t('admin.groups.smartWS.loadFailed')
  } finally {
    if (current === generation) loading.value = false
  }
}, { immediate: true })

function close() {
  if (!saving.value) emit('close')
}

async function save() {
  if (!props.group || !policy.value || saving.value) return
  const id = props.group.id
  const current = generation
  saving.value = true
  error.value = ''
  try {
    await setSmartWS(id, { enabled: enabled.value, revision: policy.value.revision })
    if (current === generation) emit('close')
  } catch (cause: unknown) {
    if (current === generation) {
      const apiError = cause as { status?: number; response?: { status?: number } }
      const status = apiError?.status ?? apiError?.response?.status
      error.value = t(status === 409 ? 'admin.groups.smartWS.conflict' : 'admin.groups.smartWS.saveFailed')
      // A timeout may follow a committed write. Reload before retrying a revision.
      policy.value = null
      try {
        const latest = await getSmartWS(id)
        if (current === generation) {
          policy.value = latest
          enabled.value = latest.enabled
        }
      } catch { /* Keep saving disabled until the dialog is reopened. */ }
    }
  } finally {
    saving.value = false
  }
}
</script>
