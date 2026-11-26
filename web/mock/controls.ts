const panel = document.createElement('details');
panel.className = 'probex-mock-controls';
panel.innerHTML = /* HTML */ `
  <summary>MOCK</summary>
  <div>
    <strong>Probex UI Mock</strong>
    <span>登录 Token：<code>mock</code></span>
    <select aria-label="Mock 数据场景">
      <option value="default">默认完整数据（重置）</option>
      <option value="offline">全部离线</option>
      <option value="online">全部在线</option>
      <option value="empty">空列表</option>
      <option value="enroll">模拟待加入节点上线</option>
    </select>
    <button type="button">应用场景</button>
    <label class="mock-network">
      <input type="checkbox" /> 弱网环境（HTTP API 延迟 1.2 秒）
    </label>
    <small
      >仅模拟数据，不执行真实命令。<br />命令输入 mock:help，终端输入
      help。</small
    >
    <span role="status"></span>
  </div>
`;
document.body.append(panel);
const weakNetwork = panel.querySelector('input')!;
weakNetwork.checked = document.cookie
  .split('; ')
  .includes('probex_mock_weak_network=1');
weakNetwork.onchange = () => {
  document.cookie = weakNetwork.checked
    ? 'probex_mock_weak_network=1; Path=/; SameSite=Strict'
    : 'probex_mock_weak_network=; Path=/; Max-Age=0; SameSite=Strict';
};
panel.querySelector('button')!.onclick = async () => {
  const response = await fetch('/api/mock/scenario', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ scenario: panel.querySelector('select')!.value }),
  });
  if (response.ok) location.reload();
  else
    panel.querySelector('[role=status]')!.textContent =
      response.status === 401 ? '请先使用 mock 登录。' : '场景切换失败。';
};
import './controls.css';
