<template>
  <div>
    <template v-if="result.output_kind === 'html' && result.output_html">
      <iframe
        ref="htmlFrame"
        class="block w-full overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-dark-700"
        scrolling="no"
        :style="{ height: `${htmlFrameHeight}px` }"
        sandbox="allow-scripts"
        referrerpolicy="no-referrer"
        :srcdoc="safeHTML"
        :title="t('tests.htmlResult')"
        @load="onHTMLLoad"
      />
    </template>
    <div v-else-if="result.output_kind === 'number' && result.output_numeric != null" class="rounded-lg bg-gray-50 p-5 text-center text-3xl font-semibold text-gray-900 dark:bg-dark-800 dark:text-white">
      {{ result.output_numeric }}
    </div>
    <pre v-else-if="result.response_text" class="whitespace-pre-wrap break-words rounded-lg bg-gray-50 p-4 text-sm dark:bg-dark-800">{{ result.response_text }}</pre>
    <p v-else-if="result.status === 'running' || result.status === 'pending'" class="flex items-center gap-2 rounded-lg bg-blue-50 px-4 py-3 text-sm text-blue-700 dark:bg-blue-950/30 dark:text-blue-300">
      <span class="h-2 w-2 animate-pulse rounded-full bg-blue-500" />
      {{ t('tests.running') }}
    </p>
    <p v-else class="text-sm text-gray-500">{{ t('tests.noOutput') }}</p>
    <details v-if="result.response_text && ['html', 'number'].includes(result.output_kind)" class="mt-3 text-sm text-gray-500">
      <summary class="cursor-pointer">{{ t('tests.rawOutput') }}</summary>
      <pre class="mt-2 whitespace-pre-wrap break-words">{{ result.response_text }}</pre>
    </details>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TestResult } from '@/types'
import { buildTestPreviewHTML } from '@/utils/testPreview'

const props = defineProps<{ result: TestResult }>()
const { t } = useI18n()
const htmlFrame = ref<HTMLIFrameElement | null>(null)
const htmlFrameHeight = ref(448)
const onPreviewMessage = (event: MessageEvent<unknown>) => {
  if (event.source !== htmlFrame.value?.contentWindow) return
  const data = event.data
  if (!data || typeof data !== 'object' || (data as { type?: unknown }).type !== 'sub2api-test-preview-size') return
  const reported = Number((data as { height?: unknown }).height)
  if (!Number.isFinite(reported) || reported <= 0) return
  // Keep a sensible lower bound while allowing the page itself to provide the
  // only scroll container. The result must remain fully readable when a test
  // returns a tall HTML/SVG document.
  // Do not shrink after a short first measurement; late-loading fonts,
  // animation layout, and responsive SVGs can report their full height later.
  htmlFrameHeight.value = Math.max(htmlFrameHeight.value, 320, Math.ceil(reported))
}
const onHTMLLoad = () => {
  // Reset while a new result is loading; the embedded script will immediately
  // report the precise document height afterwards.
  htmlFrameHeight.value = 448
}
onMounted(() => window.addEventListener('message', onPreviewMessage))
onBeforeUnmount(() => window.removeEventListener('message', onPreviewMessage))
// Render HTML results as soon as they arrive. The helper strips unsafe markup
// while keeping the test animation in an isolated sandboxed iframe.
const safeHTML = computed(() => buildTestPreviewHTML(props.result.output_html || ''))
</script>
