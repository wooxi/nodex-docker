<template>
  <div v-if="form">
    <div class="card">
      <div class="card-title">
        <el-icon><Setting /></el-icon>节点编辑：{{ form.name }}
        <el-button size="small" style="margin-left:auto" @click="$router.push('/nodes')">返回列表</el-button>
      </div>
      <el-form :model="form" label-width="130px" style="max-width:640px">
        <el-form-item label="节点名称" required>
          <el-input v-model="form.name" style="width:280px" />
        </el-form-item>
        <el-form-item label="启用节点">
          <el-switch v-model="form.enabled" />
        </el-form-item>
      </el-form>
    </div>

    <!-- 面板对接（节点级：node_id/类型每节点独立） -->
    <div class="card">
      <div class="card-title"><el-icon><Link /></el-icon>面板对接（本节点）</div>
      <el-form label-width="130px" style="max-width:640px">
        <el-form-item label="面板节点 ID" required>
          <el-input-number v-model="form.node_id" :min="1" :max="9999" />
          <span class="tip">面板后台「服务器」中的节点 ID（每节点不同）</span>
        </el-form-item>
        <el-form-item label="节点类型">
          <el-select v-model="form.node_type" placeholder="自动（推荐）" clearable style="width:220px">
            <el-option label="vless" value="vless" />
            <el-option label="vmess" value="vmess" />
            <el-option label="trojan" value="trojan" />
            <el-option label="shadowsocks" value="shadowsocks" />
            <el-option label="hysteria2" value="hysteria" />
          </el-select>
          <span class="tip">留空由面板返回的协议决定</span>
        </el-form-item>
        <el-form-item>
          <el-button :loading="testing" @click="testPanel">测试面板连接</el-button>
          <span class="tip">面板地址/密钥为全局配置（面板对接页）</span>
        </el-form-item>
      </el-form>
    </div>

    <!-- 协议选择 + 对应配置 -->
    <div class="card">
      <div class="card-title"><el-icon><Key /></el-icon>协议配置</div>

      <div class="protocol-picker">
        <div v-for="p in protocols" :key="p.value"
          class="proto-item" :class="{ active: form.node.protocol === p.value }" @click="form.node.protocol = p.value">
          <el-icon :size="20"><component :is="p.icon" /></el-icon>
          <div class="proto-name">{{ p.label }}</div>
          <div class="proto-desc">{{ p.desc }}</div>
        </div>
      </div>

      <el-form :model="form" label-width="140px" style="max-width:720px;margin-top:20px">
        <!-- ========== Xray 系协议（vless/vmess/trojan/shadowsocks） ========== -->
        <template v-if="form.node.protocol !== 'hysteria2'">
          <el-divider content-position="left">{{ protoLabel }} 入站</el-divider>
          <el-form-item v-if="!panelEnabled" label="监听端口" required>
            <el-input-number v-model="form.node.port" :min="1" :max="65535" />
          </el-form-item>
          <el-form-item v-if="['vless','vmess'].includes(form.node.protocol)" label="UUID" required>
            <el-input v-model="form.node.uuid" style="width:360px" placeholder="本地模式测试用户">
              <template #append><el-button @click="gen('uuid')"><el-icon><Refresh /></el-icon>生成</el-button></template>
            </el-input>
          </el-form-item>

          <!-- VLESS：TLS/Reality -->
          <template v-if="form.node.protocol === 'vless'">
            <el-form-item label="TLS 类型">
              <el-radio-group v-model="form.node.tls">
                <el-radio :value="0">关闭</el-radio>
                <el-radio :value="1">TLS（证书）</el-radio>
                <el-radio :value="2">Reality</el-radio>
              </el-radio-group>
              <span class="tip">面板模式以面板配置为准</span>
            </el-form-item>
            <template v-if="form.node.tls === 2">
              <el-form-item label="目标域名 (dest)" required>
                <el-input v-model="form.node.reality.dest" placeholder="www.amazon.com:443" />
                <div class="hint">建议使用未部署 MLKEM 后量子加密的站点（amazon/taobao/wikipedia 等）</div>
              </el-form-item>
              <el-form-item label="SNI 列表" required>
                <el-input v-model="form.node.reality.server_names" placeholder="www.amazon.com" />
              </el-form-item>
              <el-form-item label="私钥" required>
                <el-input v-model="form.node.reality.private_key" placeholder="X25519 私钥">
                  <template #append><el-button @click="genReality"><el-icon><Refresh /></el-icon>生成密钥对</el-button></template>
                </el-input>
                <div v-if="form.node.reality.public_key" class="pubkey">公钥: <code>{{ form.node.reality.public_key }}</code></div>
              </el-form-item>
              <el-form-item label="Short IDs">
                <el-input v-model="form.node.reality.short_ids" placeholder="留空自动" />
              </el-form-item>
            </template>
            <template v-else-if="form.node.tls === 1">
              <el-form-item label="证书路径"><el-input v-model="form.node.cert_path" /></el-form-item>
              <el-form-item label="私钥路径"><el-input v-model="form.node.key_path" /></el-form-item>
            </template>
          </template>

          <!-- VMess: TLS -->
          <template v-if="form.node.protocol === 'vmess'">
            <el-form-item label="TLS 类型">
              <el-radio-group v-model="form.node.tls">
                <el-radio :value="0">关闭</el-radio>
                <el-radio :value="1">TLS（证书）</el-radio>
              </el-radio-group>
            </el-form-item>
            <template v-if="form.node.tls === 1">
              <el-form-item label="证书路径"><el-input v-model="form.node.cert_path" placeholder="/etc/nodex/cert.pem" /></el-form-item>
              <el-form-item label="私钥路径"><el-input v-model="form.node.key_path" placeholder="/etc/nodex/key.pem" /></el-form-item>
              <el-form-item label="SNI (serverName)"><el-input v-model="form.node.server_name" /></el-form-item>
            </template>
          </template>

          <!-- Trojan: 必须 TLS -->
          <template v-if="form.node.protocol === 'trojan'">
            <el-alert type="info" :closable="false" style="margin-bottom:16px" title="Trojan 需启用 TLS，用户密码由面板 UUID 自动生成" />
            <el-form-item label="证书路径"><el-input v-model="form.node.cert_path" placeholder="/etc/nodex/cert.pem" /></el-form-item>
            <el-form-item label="私钥路径"><el-input v-model="form.node.key_path" placeholder="/etc/nodex/key.pem" /></el-form-item>
            <el-form-item label="SNI (serverName)"><el-input v-model="form.node.server_name" /></el-form-item>
          </template>

          <!-- Shadowsocks -->
          <template v-if="form.node.protocol === 'shadowsocks'">
            <el-form-item label="加密方式">
              <el-select v-model="form.node.ss_method" style="width:300px">
                <el-option label="2022-blake3-aes-128-gcm" value="2022-blake3-aes-128-gcm" />
                <el-option label="2022-blake3-aes-256-gcm" value="2022-blake3-aes-256-gcm" />
                <el-option label="aes-128-gcm" value="aes-128-gcm" />
                <el-option label="chacha20-ietf-poly1305" value="chacha20-ietf-poly1305" />
              </el-select>
            </el-form-item>
          </template>

        </template>

        <!-- ========== Hysteria2 ========== -->
        <template v-else>
          <el-divider content-position="left">Hysteria2 入站</el-divider>
          <el-form-item v-if="!panelEnabled" label="监听端口" required>
            <el-input-number v-model="form.node.hy2.port" :min="1" :max="65535" />
            <span class="tip">UDP 端口</span>
          </el-form-item>
          <el-form-item label="认证密码">
            <el-input v-model="form.node.hy2.password" style="width:360px" placeholder="本地模式使用；面板模式由用户 uuid 认证">
              <template #append><el-button @click="gen('password')"><el-icon><Refresh /></el-icon>生成</el-button></template>
            </el-input>
          </el-form-item>
          <el-form-item label="混淆 (obfs)">
            <el-radio-group v-model="form.node.hy2.obfs">
              <el-radio value="none">关闭</el-radio>
              <el-radio value="salamander">salamander</el-radio>
            </el-radio-group>
          </el-form-item>
          <el-form-item v-if="form.node.hy2.obfs === 'salamander'" label="混淆密码">
            <el-input v-model="form.node.hy2.obfs_password" style="width:360px">
              <template #append><el-button @click="gen('hex8')"><el-icon><Refresh /></el-icon>生成</el-button></template>
            </el-input>
          </el-form-item>
          <el-form-item label="上下行带宽">
            <el-input-number v-model="form.node.hy2.up_mbps" :min="1" :max="10000" /> /
            <el-input-number v-model="form.node.hy2.down_mbps" :min="1" :max="100000" /> Mbps
          </el-form-item>
          <el-form-item label="忽略客户端带宽">
            <el-switch v-model="form.node.hy2.ignore_bw" />
          </el-form-item>
          <el-form-item label="证书/私钥">
            <el-input v-model="form.node.hy2.cert_path" placeholder="留空用全局证书" style="width:340px;margin-bottom:6px" />
            <el-input v-model="form.node.hy2.key_path" placeholder="留空用全局私钥" style="width:340px" />
          </el-form-item>
        </template>

        <el-form-item style="margin-top:20px">
          <el-button type="primary" :loading="saving" @click="save">保存配置</el-button>
          <el-button :loading="saving" @click="saveAndRestart">保存并重启节点</el-button>
        </el-form-item>
      </el-form>
    </div>

    <!-- 转发出站 -->
    <div class="card">
      <div class="card-title"><el-icon><Share /></el-icon>转发出站（XrayR 转发模式）</div>
      <el-form label-width="130px" style="max-width:720px">
        <el-form-item label="启用转发">
          <el-switch v-model="form.forward.enabled" />
          <span class="tip">入站流量转发到落地节点（替代直连）</span>
        </el-form-item>
        <template v-if="form.forward.enabled">
          <el-form-item label="落地 UUID" required>
            <el-input v-model="form.forward.uuid" style="width:340px" placeholder="落地节点 VLESS UUID" />
          </el-form-item>
          <el-form-item label="SNI" required>
            <el-input v-model="form.forward.server_name" style="width:340px" placeholder="chongya.ccwu.cc" />
          </el-form-item>
          <el-form-item label="WS 路径">
            <el-input v-model="form.forward.ws_path" style="width:340px" placeholder="/proxyip=..." />
          </el-form-item>
          <el-form-item label="WS Host">
            <el-input v-model="form.forward.ws_host" style="width:340px" placeholder="chongya.ccwu.cc" />
          </el-form-item>
          <el-form-item label="目标服务器">
            <el-input v-model="targetsText" type="textarea" :rows="5" style="width:340px" placeholder="每行一个：IP 或 IP:端口 或 IP:端口:权重" />
          </el-form-item>
          <div class="tip" style="margin-left:130px">多目标自动负载均衡（随机）</div>
        </template>
      </el-form>
    </div>
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage } from 'element-plus'
import { Setting, Key, Refresh, Lightning, Lock, Connection, Share, Link } from '@element-plus/icons-vue'
import { api } from '../api'

const route = useRoute()
const protocols = [
  { value: 'vless', label: 'VLESS', desc: 'Reality/WS', icon: Lightning },
  { value: 'vmess', label: 'VMess', desc: '经典协议', icon: Key },
  { value: 'trojan', label: 'Trojan', desc: 'TLS 伪装', icon: Lock },
  { value: 'shadowsocks', label: 'SS', desc: '轻量快速', icon: Connection },
  { value: 'hysteria2', label: 'Hysteria2', desc: 'UDP 加速', icon: Share }
]

const protoLabel = computed(() => {
  const p = protocols.find(x => x.value === form.value?.node.protocol)
  return p ? p.label : ''
})

const form = ref(null)
const saving = ref(false)
const testing = ref(false)
const panelEnabled = ref(false)
const targetsText = ref('')

async function testPanel() {
  testing.value = true
  try {
    const cfg = await api.get('/api/config')
    const res = await api.post('/api/nodes/test', {
      url: cfg.panel.url || '',
      token: cfg.panel.token || '',
      node_id: form.value.node_id || 0,
      node_type: form.value.node_type || ''
    })
    ElMessage.success(res.message)
  } catch (e) { ElMessage.error(e.message) } finally { testing.value = false }
}

onMounted(async () => {
  try {
    const cfg = await api.get('/api/config')
    panelEnabled.value = !!cfg.panel.enabled
    const n = cfg.nodes.find(x => x.id === route.params.id)
    if (!n) { ElMessage.error('节点不存在'); return }
    form.value = reactive(JSON.parse(JSON.stringify(n)))
    // 确保 forward 存在
    if (!form.value.forward) form.value.forward = { enabled: false, targets: [], fingerprint: 'chrome' }
    targetsText.value = (form.value.forward.targets || []).map(t => t.address + (t.port ? ':' + t.port : '') + (t.weight && t.weight !== 1 ? ':' + t.weight : '')).join('\n')
  } catch (e) { ElMessage.error(e.message) }
})

async function save(restart = false) {
  saving.value = true
  try {
    // 解析目标服务器列表
    form.value.forward.targets = targetsText.value.split('\n').map(ln => ln.trim()).filter(Boolean).map(ln => {
      const parts = ln.split(':')
      return { address: parts[0], port: parseInt(parts[1], 10) || 0, weight: parseInt(parts[2], 10) || 1 }
    })
    const cfg = await api.get('/api/config')
    const idx = cfg.nodes.findIndex(x => x.id === form.value.id)
    if (idx < 0) throw new Error('节点不存在')
    cfg.nodes[idx] = JSON.parse(JSON.stringify(form.value))
    await api.put('/api/config', cfg)
    ElMessage.success('配置已保存')
    if (restart) {
      await api.post('/api/action', { action: 'restart', node_id: form.value.id })
      ElMessage.success('节点已重启')
    }
  } catch (e) { ElMessage.error(e.message) } finally { saving.value = false }
}

async function gen(type) {
  try {
    const res = await api.post('/api/generate', { type })
    if (type === 'uuid') form.value.node.uuid = res.value
    if (type === 'password') form.value.node.hy2.password = res.value
    if (type === 'hex8') form.value.node.hy2.obfs_password = res.value
  } catch (e) { ElMessage.error(e.message) }
}

async function genReality() {
  try {
    const res = await api.post('/api/generate', { type: 'reality' })
    form.value.node.reality.private_key = res.privateKey
    form.value.node.reality.public_key = res.publicKey
    form.value.node.reality.short_ids = res.shortId
    ElMessage.success('已生成 Reality 密钥对')
  } catch (e) { ElMessage.error(e.message) }
}
</script>

<style scoped>
.protocol-picker { display: grid; grid-template-columns: repeat(5, 1fr); gap: 12px; }
.proto-item { border: 2px solid #e4e7ed; border-radius: 8px; padding: 14px 10px; text-align: center; cursor: pointer; transition: all .2s; }
.proto-item:hover { border-color: #409eff; }
.proto-item.active { border-color: #409eff; background: #ecf5ff; }
.proto-name { font-weight: 600; margin: 8px 0 4px; font-size: 14px; }
.proto-desc { color: #909399; font-size: 12px; }
.tip { color: #909399; font-size: 12px; margin-left: 8px; }
.hint { width: 100%; font-size: 12px; color: #909399; margin-top: 4px; }
.pubkey { width: 100%; font-size: 12px; color: #67c23a; margin-top: 4px; }
.pubkey code { background: #f0f9eb; padding: 2px 6px; border-radius: 4px; }
@media (max-width: 900px) { .protocol-picker { grid-template-columns: repeat(2, 1fr); } }
</style>
