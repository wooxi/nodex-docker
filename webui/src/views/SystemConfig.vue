<template>
  <div>
    <div class="card">
      <div class="card-title"><el-icon><Tools /></el-icon>系统设置（全局）</div>
      <el-form :model="form" label-width="140px" style="max-width:640px">
        <el-form-item label="Web 管理端口">
          <el-input-number v-model="form.web.port" :min="1" :max="65535" />
          <span class="tip">修改后重启 nodex 生效</span>
        </el-form-item>
        <el-form-item label="xray 路径">
          <el-input v-model="form.system.xray_path" style="width:320px" />
          <span v-if="cores.xray" class="core-info">
            <el-tag :type="cores.xray.installed ? 'success' : 'danger'" size="small">{{ cores.xray.installed ? cores.xray.version : '未安装' }}</el-tag>
            <el-button size="small" :loading="updating === 'xray'" @click="updateCore('xray')">{{ cores.xray.installed ? '更新' : '下载' }}</el-button>
          </span>
        </el-form-item>
        <el-form-item label="hysteria 路径">
          <el-input v-model="form.system.hysteria_path" style="width:320px" />
          <span v-if="cores.hysteria" class="core-info">
            <el-tag :type="cores.hysteria.installed ? 'success' : 'danger'" size="small">{{ cores.hysteria.installed ? cores.hysteria.version : '未安装' }}</el-tag>
            <el-button size="small" :loading="updating === 'hysteria'" @click="updateCore('hysteria')">{{ cores.hysteria.installed ? '更新' : '下载' }}</el-button>
          </span>
        </el-form-item>
        <el-form-item label="日志级别">
          <el-select v-model="form.system.log_level" style="width:200px">
            <el-option label="debug" value="debug" />
            <el-option label="info" value="info" />
            <el-option label="warning" value="warning" />
            <el-option label="error" value="error" />
          </el-select>
        </el-form-item>
        <el-form-item label="hysteria 证书">
          <el-input v-model="form.system.cert_path" placeholder="/etc/nodex/hy2.crt" style="width:320px;margin-bottom:6px" />
          <el-input v-model="form.system.key_path" placeholder="/etc/nodex/hy2.key" style="width:320px" />
          <div class="tip" style="width:100%">节点未单独配置证书时使用；留空则 hysteria2 不启动</div>
        </el-form-item>
        <el-form-item label="API 起始端口">
          <el-input-number v-model="form.system.api_port_base" :min="10000" :max="60000" />
          <span class="tip">xray gRPC（每节点 +1）</span>
          <el-input-number v-model="form.system.hy2_api_port_base" :min="8000" :max="60000" style="margin-left:12px" />
          <span class="tip">hysteria traffic API（每节点 +1）</span>
        </el-form-item>
        <el-form-item label="数据目录">
          <el-input v-model="form.system.data_dir" style="width:320px" disabled />
        </el-form-item>
        <el-form-item>
          <el-button type="primary" :loading="saving" @click="save">保存</el-button>
          <span class="tip">保存后需在节点管理页重启节点生效</span>
        </el-form-item>
      </el-form>
    </div>

    <div class="card">
      <div class="card-title"><el-icon><Lock /></el-icon>修改管理密码</div>
      <el-form label-width="140px" style="max-width:640px" @submit.prevent>
        <el-form-item label="新密码" required>
          <el-input v-model="newPwd" type="password" show-password style="width:320px" />
        </el-form-item>
        <el-form-item>
          <el-button type="warning" :loading="changing" @click="changePwd">修改密码</el-button>
        </el-form-item>
      </el-form>
    </div>

    <div class="card">
      <div class="card-title"><el-icon><InfoFilled /></el-icon>关于</div>
      <el-descriptions :column="1" size="small" border style="max-width:640px">
        <el-descriptions-item label="NodeX 版本">v0.2.0（多节点）</el-descriptions-item>
        <el-descriptions-item label="功能">多节点管理 · Xray (vless/vmess/trojan/ss) + Hysteria2 · Xboard 对接 · Docker/OpenWrt 部署</el-descriptions-item>
        <el-descriptions-item label="配置文件">{{ form.system.data_dir }}/config.json</el-descriptions-item>
      </el-descriptions>
    </div>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { Tools, Lock, InfoFilled } from '@element-plus/icons-vue'
import { api } from '../api'

const form = reactive({
  web: { port: 8888 },
  system: { xray_path: '', hysteria_path: '', log_level: 'info', data_dir: '', cert_path: '', key_path: '', api_port_base: 10085, hy2_api_port_base: 8444 }
})
const newPwd = ref('')
const saving = ref(false)
const changing = ref(false)
const cores = ref({})
const updating = ref('')

async function loadCores() {
  try {
    const [x, h] = await Promise.all([
      api.get('/api/core/info?type=xray'),
      api.get('/api/core/info?type=hysteria')
    ])
    cores.value = { xray: x, hysteria: h }
  } catch (e) {}
}

async function updateCore(kind) {
  updating.value = kind
  try {
    const res = await api.post('/api/core/update', { type: kind })
    ElMessage.success(`${kind} 已更新至 ${res.version}`)
    loadCores()
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    updating.value = ''
  }
}

onMounted(async () => {
  try {
    const cfg = await api.get('/api/config')
    Object.assign(form.web, cfg.web)
    Object.assign(form.system, cfg.system)
  } catch (e) {}
  loadCores()
})

async function save() {
  saving.value = true
  try {
    const cfg = await api.get('/api/config')
    cfg.web = { ...form.web }
    cfg.system = { ...form.system }
    await api.put('/api/config', cfg)
    ElMessage.success('已保存')
  } catch (e) { ElMessage.error(e.message) } finally { saving.value = false }
}

async function changePwd() {
  if (newPwd.value.length < 6) { ElMessage.warning('密码至少 6 位'); return }
  changing.value = true
  try {
    const cfg = await api.get('/api/config')
    cfg.web.password = newPwd.value
    await api.put('/api/config', cfg)
    ElMessage.success('密码已修改')
    newPwd.value = ''
  } catch (e) { ElMessage.error(e.message) } finally { changing.value = false }
}
</script>

<style scoped>
.tip { color: #909399; font-size: 12px; margin-left: 8px; }
</style>
