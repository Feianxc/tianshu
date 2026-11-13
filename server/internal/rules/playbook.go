package rules

// Playbook says what a finding means and how to work on it. The hub is read-only, so
// the commands are for the owner to run on the host; they only read. An agent that
// has this can explain the next step instead of only repeating the alert.
type Playbook struct {
	Meaning  string   `json:"meaning"`
	Steps    []string `json:"steps"`
	Commands []string `json:"commands,omitempty"`
}

var playbooks = map[string]Playbook{
	"service.down": {
		Meaning: "外部探测或主机上的运行单元表明这个服务现在不可用。",
		Steps: []string{
			"先看 probes：失败属于哪一类——连接被拒绝（进程没在监听）、超时（网络或防火墙）、DNS 解析失败、证书错误，还是返回了非预期的状态码。",
			"再看 hosts：主机是否在线，磁盘和内存是否耗尽。主机本身有问题时先处理主机。",
			"然后看 logs：失败单元的最后几行通常直接写明原因，例如端口被占用、配置文件错误、依赖没有就绪、被 OOM 终止。",
			"如果故障出现在一次发布之后，对照发布时间，优先考虑回退到上一版。",
			"修复后等一轮探测（约 1 分钟），服务恢复后这条待处理会自动关闭。",
		},
		Commands: []string{"systemctl status <unit> --no-pager", "journalctl -u <unit> -n 80 --no-pager", "ss -ltnp", "curl -sv <服务地址> -o /dev/null"},
	},
	"host.failed_units": {
		Meaning: "主机上有 systemd 单元处于 failed 状态。它可能是真实故障，也可能是早已不用的遗留单元。",
		Steps: []string{
			"在 logs 里按单元查看最后几行，判断是启动失败、运行中崩溃，还是一次性任务执行失败。",
			"对照目录：这个单元是否属于某个登记过的服务。没有登记、也不再需要的，是遗留单元。",
			"真实故障：按日志修复后 systemctl restart；遗留单元：systemctl disable 后执行 systemctl reset-failed 清除失败标记，否则它会一直掩盖新的故障。",
		},
		Commands: []string{"systemctl --failed --no-pager", "systemctl status <unit> --no-pager", "journalctl -u <unit> -n 50 --no-pager"},
	},
	"host.lost": {
		Meaning: "这台常开设备超过预期时间没有任何上报，从外部也没有它仍在工作的证据。",
		Steps: []string{
			"先确认是主机真的不可达，还是只有采集器停了：看这台主机上的服务探测是否仍然成功。",
			"从另一个网络 ping 或 SSH 这台主机；云主机到服务商控制台看实例状态和监控。",
			"主机在线但没有上报时，检查采集器进程和它到天枢的网络。",
		},
		Commands: []string{"systemctl status wanglou-agent --no-pager", "journalctl -u wanglou-agent -n 30 --no-pager"},
	},
	"host.feed_lost": {
		Meaning: "设备本身还有在线的证据（上面的服务探测成功），但它的采集数据断了，现在看不到它的资源和单元状态。",
		Steps: []string{
			"检查负责这台设备的采集器是否在运行：本机采集器进程，或发起 SSH 采集的那台电脑是否开机。",
			"查看采集器日志里的失败原因：SSH 连接被拒、密钥失效、令牌过期，或到天枢的网络不通。",
		},
		Commands: []string{"systemctl status wanglou-agent --no-pager", "journalctl -u wanglou-agent -n 30 --no-pager"},
	},
	"host.memory": {
		Meaning: "内存占用长时间处于高位，继续上涨会触发 OOM，系统会随机终止进程。",
		Steps: []string{
			"找出占用最多的进程，判断是正常负载还是泄漏（占用只增不减）。",
			"在 logs 里搜索 oom 或 Out of memory，确认是否已经发生过终止。",
			"短期可以重启占用异常的服务；长期给该服务设置 MemoryMax，或扩容。",
		},
		Commands: []string{"free -m", "ps aux --sort=-rss | head -15", "journalctl -k --since -1d | grep -i -E 'oom|out of memory'"},
	},
	"host.disk": {
		Meaning: "磁盘剩余空间不足。写满后数据库、日志和发布都会失败，而且往往是同时失败。",
		Steps: []string{
			"先找出增长最快的目录，通常是日志、旧的发布版本、构建缓存、容器镜像或备份。",
			"清理前确认内容不再需要；数据库和用户上传的数据不要按目录直接删除。",
			"找到持续增长的根因并加上保留策略，否则清理之后还会再满。",
		},
		Commands: []string{"df -h", "du -xh --max-depth=2 / 2>/dev/null | sort -h | tail -25", "journalctl --disk-usage"},
	},
	"host.ssh_password": {
		Meaning: "这台主机允许用密码登录 SSH，正在暴露在撞库之下。它不影响运行，所以健康状态不会因此变化。",
		Steps: []string{
			"先确认至少有一把可用的密钥，并且云控制台（VNC）可以登录，作为出错时的退路。",
			"在 sshd_config.d 里新增一个排在最前面的文件，设置 PasswordAuthentication no 和 PermitRootLogin prohibit-password。sshd 对每个选项取第一次出现的值，所以直接改主配置可能不生效。",
			"挂一个定时自动回滚，再用 sshd -t 检查后 reload，不要 restart；用新连接验证密钥能登录、密码被拒绝之后，取消自动回滚。",
		},
		Commands: []string{"sshd -T | grep -E '^(passwordauthentication|permitrootlogin) '", "ls -la /etc/ssh/sshd_config.d/"},
	},
	"host.ssh_new_key": {
		Meaning: "开始观察之后，这台主机上多了一把可以登录的钥匙。如果不是你自己加的，按入侵处理。",
		Steps: []string{
			"先回忆最近是否自己加过钥匙，或有部署工具加过。",
			"不是自己加的：立即从 authorized_keys 移除它，查看登录记录里它用过的来源和时间，再检查同一时间段主机上的其他改动。",
			"确认是自己加的：这条待处理在 7 天后自动消失，也可以稍后提醒。",
		},
		Commands: []string{"ssh-keygen -lf ~/.ssh/authorized_keys", "last -F -n 20", "journalctl -u ssh -u sshd --since -7d | grep Accepted | tail -20"},
	},
	"host.ssh_shared_key": {
		Meaning: "同一把钥匙能登录不同空间的主机，其中一处泄露，另一处也随之暴露。",
		Steps: []string{
			"为每个空间生成独立的钥匙。",
			"先把新公钥加到主机并验证能登录，再移除共用的那一把，任何时候都保留一把可用的钥匙。",
		},
	},
	"tls.expiring": {
		Meaning: "证书即将到期。到期后浏览器和客户端会直接拒绝连接。",
		Steps: []string{
			"确认自动续期是否在工作：查看 certbot、caddy 或 acme 相关的日志。",
			"续期失败的常见原因是验证路径不通：HTTP-01 需要 80 端口可达，DNS-01 需要 DNS 接口的凭据仍然有效。",
			"续期后重新加载使用该证书的服务，并等待下一轮 TLS 探测确认。",
		},
		Commands: []string{"echo | openssl s_client -connect <域名>:443 -servername <域名> 2>/dev/null | openssl x509 -noout -dates"},
	},
	"coverage.no_check": {
		Meaning: "这个服务有对外地址，但没有配置探测，它不可用时天枢不会知道。",
		Steps:   []string{"在目录文件里给这个服务加一条 checks（http、tcp 或 tls），重新导入。"},
	},
	"coverage.unmonitored": {
		Meaning: "这个对象没有任何数据来源，状态只能显示为未知。",
		Steps:   []string{"给所在主机安装采集器或配置 SSH 只读采集；手机可以用天枢的安卓应用上报。", "确实不需要监控的，把生命周期改为暂停或退役。"},
	},
	"project.blocked":      {Meaning: "项目登记了阻塞项。", Steps: []string{"看阻塞项的负责人和内容，判断是等别人还是等自己。", "解除后在项目里删掉这条阻塞。"}},
	"project.no_next_step": {Meaning: "进行中的项目没有写下一步，下次回来时不知道从哪里继续。", Steps: []string{"写一句下次回来第一件要做的事。"}},
	"project.overdue":      {Meaning: "项目已经超过计划日期。", Steps: []string{"重新估计日期，或缩小范围；确认不再做的，把阶段改为暂停。"}},
	"repo.ci_failed": {
		Meaning: "默认分支最近一次构建失败。",
		Steps:   []string{"打开仓库的 Actions 页面看失败的步骤和日志。", "区分是代码问题、依赖或环境问题，还是偶发失败；偶发的重跑一次。"},
	},
	"repo.unpushed": {Meaning: "本地有提交没有推送，只存在于这一台电脑上。", Steps: []string{"确认内容可以公开或推到私有远端后推送；还不想推的，至少做一次备份。"}, Commands: []string{"git status -sb", "git log --oneline @{u}.. | head"}},
	"repo.dirty":    {Meaning: "本地有大量未提交的改动。", Steps: []string{"提交仍然需要的改动，丢弃试验性的改动，避免和后续修改混在一起。"}, Commands: []string{"git status --short | head -40"}},
	"credential.rotation_due": {
		Meaning: "凭据到了计划的轮换日期。",
		Steps:   []string{"在签发方生成新的凭据，更新使用它的地方并验证，再吊销旧的。", "更新目录里的计划轮换日期。"},
	},
	"credential.cross_workspace": {Meaning: "一个凭据被不同空间的对象共用。", Steps: []string{"为每个空间签发独立的凭据，先加后删。"}},
	"credential.plaintext": {
		Meaning: "凭据以明文文件保存，任何能读这个目录的程序都能读到它。",
		Steps:   []string{"迁入密码管理器。", "因为它曾经以明文存在，迁移后要轮换一次。"},
	},
}

// PlaybookFor returns the playbook of a rule.
func PlaybookFor(rule string) (Playbook, bool) {
	p, ok := playbooks[rule]
	return p, ok
}
