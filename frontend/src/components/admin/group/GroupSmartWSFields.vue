<template>
  <section class="mt-4 border-t border-gray-200 pt-4 dark:border-dark-400" data-testid="group-smart-ws-settings">
    <h4 class="mb-1 text-sm font-medium text-gray-700 dark:text-gray-300">
      {{ t('admin.groups.smartWS.title') }}
    </h4>
    <p class="mb-3 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.groups.smartWS.description') }}</p>
    <p v-if="loading" role="status" class="mb-2 text-sm text-gray-500">{{ t('common.loading') }}</p>
    <p v-if="error" role="alert" class="mb-2 text-sm text-red-600 dark:text-red-400">{{ error }}</p>
    <label class="flex items-center gap-3 text-sm text-gray-700 dark:text-gray-300">
      <input
        v-model="enabled"
        type="checkbox"
        data-testid="edit-smart-ws-enabled"
        :disabled="disabled || loading || saving || !policy"
      />
      {{ t('admin.groups.smartWS.enable') }}
    </label>
    <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.groups.smartWS.hint') }}</p>
  </section>
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getSmartWS, setSmartWS, type SmartWSPolicy } from '@/api/admin/groups'

const props = defineProps<{ groupId: number; disabled?: boolean }>()
const { t } = useI18n()
const policy = ref<SmartWSPolicy | null>(null)
const enabled = ref(false)
const loading = ref(false)
const saving = ref(false)
const error = ref('')
const saveUnconfirmed = ref(false)
let generation = 0

watch(() => props.groupId, async (id) => {
  const current = ++generation
  policy.value = null
  enabled.value = false
  error.value = ''
  saveUnconfirmed.value = false
  saving.value = false
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

onBeforeUnmount(() => { generation++ })

function validate(): boolean {
  return !loading.value && !saving.value && !saveUnconfirmed.value
}

async function save(): Promise<boolean> {
  if (!validate()) return false
  // An unavailable, untouched setting must not prevent editing other group fields.
  if (!policy.value || enabled.value === policy.value.enabled) return true
  const id = props.groupId
  const current = generation
  saving.value = true
  error.value = ''
  try {
    const result = await setSmartWS(id, { enabled: enabled.value, revision: policy.value.revision })
    if (current !== generation) return false
    policy.value = result
    enabled.value = result.enabled
    return true
  } catch (cause: unknown) {
    if (current === generation) {
      const apiError = cause as { status?: number; response?: { status?: number } }
      const status = apiError?.status ?? apiError?.response?.status
      error.value = t(status === 409 ? 'admin.groups.smartWS.conflict' : 'admin.groups.smartWS.saveFailed')
      // A timeout may follow a committed write. Reload before retrying a revision.
      policy.value = null
      saveUnconfirmed.value = true
      try {
        const latest = await getSmartWS(id)
        if (current === generation) {
          policy.value = latest
          enabled.value = latest.enabled
          saveUnconfirmed.value = false
        }
      } catch {
        if (current === generation) error.value += ` ${t('admin.groups.smartWS.loadFailed')}`
      }
    }
    return false
  } finally {
    if (current === generation) saving.value = false
  }
}

defineExpose({ loading, validate, save })
</script>
