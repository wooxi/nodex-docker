<template>
  <div>
    <div class="card">
      <div class="card-title"><el-icon><Link /></el-icon>面板对接（全局配置）</div>
      <el-alert type="info" :closable="false" style="margin-bottom:16px"
        title="面板对接是全局配置：本部署的所有节点共享同一面板配置（每个节点对应面板中的不同节点 ID）。" />
      <el-form :model="form" label-width="130px" style="max-width:640px">
        <el-form-item label="启用面板对接">
          <el-switch v-model="form.enabled" />
        </el-form-item>
        <template v-if="form.enabled">
          <el-form-item label="面板地址" required>
            <el-input v-model="form.url" placeholder="http://panel.example.com" />
          </el-form-item>
          <el-form-item label="通信密钥" required>
            <el-input v-model="form.token" placeholder="面板后台生成的通信密钥" show-password />
          </el-form-item>
          <el-alert type="info" :closable="false" style="margin-bottom:16px"
            title="节点 ID 与节点类型在「节点管理 → 编辑节点」中单独配置（每节点不同）" />
          <el-form-item label="拉取/上报间隔">
            <el-input-number v-model="form.pull_interval" :min="10" :max="600" /> 秒 /
            <el-input-number v-model="form.push_interval" :min="10" :max="600" /> 秒
          </el-form-item>
          <el-form-item>
            <el-button :loading="testing" @click="test">测试面板连接</el-button>
          </el-form-item>
        </template>
        <el-form-item v-else>
          <span class="tip">关闭后节点以本地模式运行（单测试用户）</span>
        </el-form-item>
        <el-form-item>
          <el-button type="primary" :loading="saving" @click="save">保存配置</el-button>
          <span class="tip">保存后需在节点管理页重启节点生效</span>
        </el-form-item>
      </el-form>
    </div>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { Link } from '@element-plus/icons-vue'
import { api } from '../api'

const form = reactive({ enabled: false, url: '', token: '', pull_interval: 60, push_interval: 60 })
const saving = ref(false)
const testing = ref(false)

onMounted(async () => {
  try {
    const cfg = await api.get('/api/config')
    Object.assign(form, cfg.panel)
  } catch (e) {}
})

async function save() {
  saving.value = true
  try {
    const cfg = await api.get('/api/config')
    cfg.panel = { ...form }
    await api.put('/api/config', cfg)
    ElMessage.success('配置已保存')
  } catch (e) { ElMessage.error(e.message) } finally { saving.value = false }
}

async function test() {
  testing.value = true
  try {
    const res = await api.post('/api/nodes/test', form)
    ElMessage.success(res.message)
  } catch (e) { ElMessage.error(e.message) } finally { testing.value = false }
}
</script>

<style scoped>
.tip { color: #909399; font-size: 12px; margin-left: 8px; }
</style>
