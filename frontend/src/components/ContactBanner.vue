<script setup lang="ts">
import { ref } from 'vue'
import { copyText } from '../api/client'
import { useToast } from '../stores/toast'

const groupNumber = '1072353908'
const qqGroupLink = `mqqapi://card/show_pslcard?src_type=internal&version=1&uin=${groupNumber}&card_type=group&source=qrcode`
const dialog = ref<HTMLDialogElement | null>(null)
const toast = useToast()

function openContact() {
  if (!dialog.value?.open) dialog.value?.showModal()
}

function closeContact() {
  dialog.value?.close()
}

async function copyGroupNumber() {
  try {
    await copyText(groupNumber)
    toast.show('群号已复制', 'success')
  } catch (error) {
    toast.show(error instanceof Error ? error.message : '复制失败', 'error')
  }
}

function closeFromBackdrop(event: MouseEvent) {
  if (event.target === dialog.value) closeContact()
}
</script>

<template>
  <aside class="contact-banner" aria-label="QQ 交流群">
    <div class="contact-banner__inner">
      <span class="contact-banner__icon" aria-hidden="true">
        <svg viewBox="0 0 24 24"><path d="M5 5.5h14v9H9.8L6 18v-3.5H5v-9Zm2 2v5h1v1.1l1.2-1.1H17v-5H7Zm2 1.5h2v2H9V9Zm4 0h2v2h-2V9Z" /></svg>
      </span>
      <div class="contact-banner__copy">
        <span>QQ 交流群</span>
        <strong>{{ groupNumber }}</strong>
      </div>
      <div class="contact-banner__actions">
        <button type="button" class="contact-banner__copy-button" aria-label="复制 QQ 群号" title="复制群号" @click="copyGroupNumber">
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M8 7V4h12v12h-3v4H4V7h4Zm2 0h7v7h1V6h-8v1Zm5 2H6v9h9V9Z" /></svg>
          <span>复制群号</span>
        </button>
        <button type="button" class="contact-banner__open" @click="openContact">联系</button>
      </div>
    </div>
  </aside>

  <Teleport to="body">
    <dialog ref="dialog" class="contact-dialog" aria-labelledby="contact-dialog-title" @click="closeFromBackdrop">
      <header class="contact-dialog__head">
        <div class="contact-dialog__mark" aria-hidden="true">
          <svg viewBox="0 0 24 24"><path d="M5 5.5h14v9H9.8L6 18v-3.5H5v-9Zm2 2v5h1v1.1l1.2-1.1H17v-5H7Zm2 1.5h2v2H9V9Zm4 0h2v2h-2V9Z" /></svg>
        </div>
        <div>
          <h2 id="contact-dialog-title">联系</h2>
          <p>QQ 交流群</p>
        </div>
        <button type="button" class="contact-dialog__close" aria-label="关闭" @click="closeContact">
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m6 6 12 12M18 6 6 18" /></svg>
        </button>
      </header>

      <div class="contact-dialog__number">
        <span>群号</span>
        <strong>{{ groupNumber }}</strong>
      </div>

      <footer class="contact-dialog__actions">
        <button type="button" class="contact-dialog__copy" @click="copyGroupNumber">复制群号</button>
        <a :href="qqGroupLink" class="contact-dialog__open-qq">打开 QQ</a>
      </footer>
    </dialog>
  </Teleport>
</template>
