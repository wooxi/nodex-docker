<template>
  <div class="login-wrap">
    <div class="login-box">
      <div class="login-logo">
        <el-icon :size="36" color="#409eff"><Connection /></el-icon>
        <h2>NodeX 节点管家</h2>
        <p>OpenWrt 节点管理 · Xray + Hysteria2 · Xboard 对接</p>
      </div>
      <el-form @submit.prevent="doLogin">
        <el-form-item>
          <el-input v-model="password" type="password" placeholder="管理密码" size="large" show-password @keyup.enter="doLogin" />
        </el-form-item>
        <el-button type="primary" size="large" style="width:100%" :loading="loading" @click="doLogin">登 录</el-button>
        <div v-if="error" class="err">{{ error }}</div>
        <div class="hint">首次使用：输入任意 6 位以上密码即可完成初始化</div>
      </el-form>
    </div>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { Connection } from '@element-plus/icons-vue'
import { api, setToken } from '../api'

const router = useRouter()
const password = ref('')
const loading = ref(false)
const error = ref('')

async function doLogin() {
  if (!password.value) return
  loading.value = true
  error.value = ''
  try {
    const res = await api.post('/api/login', { password: password.value })
    setToken(res.token)
    router.push('/')
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}
</script>

<style scoped>
.login-wrap { height: 100vh; display: flex; align-items: center; justify-content: center;
  background: linear-gradient(135deg, #1d2129 0%, #2b3245 100%); }
.login-box { width: 380px; background: #fff; border-radius: 12px; padding: 40px 36px; box-shadow: 0 8px 32px rgba(0,0,0,.3); }
.login-logo { text-align: center; margin-bottom: 28px; }
.login-logo h2 { margin: 10px 0 6px; font-size: 20px; }
.login-logo p { color: #909399; font-size: 12px; }
.err { color: #f56c6c; font-size: 13px; margin-top: 12px; text-align: center; }
.hint { color: #c0c4cc; font-size: 12px; margin-top: 14px; text-align: center; }
</style>
