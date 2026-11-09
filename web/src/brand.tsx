// 天枢的品牌元素。
// 天枢是北斗七星的第一颗（大熊座 α），古人沿天璇到天枢的连线找到北极星。
// 标志就是北斗七星的发丝线轮廓，只点亮天枢；它用暖金色，因为真实的天枢是一颗橙色巨星。

export const BRAND = { zh: "天枢", en: "TIANSHU" } as const;

// 七颗星在 32×32 画布上的位置，按真实赤经赤纬投影后缩放，不是随手摆的。
const DIPPER = {
  dubhe: [28.3, 9.5], // 天枢
  merak: [28.5, 15.4], // 天璇
  phecda: [20.3, 18.3], // 天玑
  megrez: [16.9, 14.7], // 天权
  alioth: [10.8, 15.8], // 玉衡
  mizar: [6.1, 17.0], // 开阳
  alkaid: [2.3, 23.2], // 摇光
} as const;

const ORDER = ["alkaid", "mizar", "alioth", "megrez", "phecda", "merak", "dubhe", "megrez"] as const;
const path = (k: number, dx = 0, dy = 0) =>
  ORDER.map((n, i) => `${i ? "L" : "M"}${(DIPPER[n][0] * k + dx).toFixed(1)} ${(DIPPER[n][1] * k + dy).toFixed(1)}`).join("");

export function Mark({ className = "mark" }: { className?: string }) {
  return (
    <svg className={className} viewBox="0 0 32 32" aria-hidden="true">
      <path className="mark-line" d={path(1)} />
      {(["merak", "phecda", "megrez", "alioth", "mizar", "alkaid"] as const).map((n) => (
        <circle key={n} className="mark-star" cx={DIPPER[n][0]} cy={DIPPER[n][1]} r="1.35" />
      ))}
      <circle className="mark-halo" cx={DIPPER.dubhe[0]} cy={DIPPER.dubhe[1]} r="4.6" />
      <circle className="mark-lamp" cx={DIPPER.dubhe[0]} cy={DIPPER.dubhe[1]} r="2.5" />
    </svg>
  );
}

export function Wordmark({ sub = true }: { sub?: boolean }) {
  return (
    <span className="wordmark">
      <b>{BRAND.zh}</b>
      {sub && <small>{BRAND.en}</small>}
    </span>
  );
}

// 背景星的位置用固定种子生成，每次打开都一样，像一张真的星图而不是随机噪点。
function seeded(seed: number) {
  let n = seed;
  return () => {
    n = (n * 1664525 + 1013904223) % 4294967296;
    return n / 4294967296;
  };
}

const NAMES: Record<keyof typeof DIPPER, string> = {
  dubhe: "天枢", merak: "天璇", phecda: "天玑", megrez: "天权", alioth: "玉衡", mizar: "开阳", alkaid: "摇光",
};

// 登录页的星图：北斗七星、指极线和北极星，配一圈赤纬刻度。
export function SkyChart() {
  // 北极星到天枢约是天璇到天枢的五倍远，七星要放得够低，北极星才在画面里。
  const k = 10, dx = 147, dy = 235;
  const at = (n: keyof typeof DIPPER) => [DIPPER[n][0] * k + dx, DIPPER[n][1] * k + dy] as const;
  const [mx, my] = at("merak"), [ux, uy] = at("dubhe");
  // 指极线：天璇到天枢的方向延长约五倍，就是北极星。
  const px = ux + (ux - mx) * 4.6, py = uy + (uy - my) * 4.6 + 0;
  const rnd = seeded(20261108);
  const field = Array.from({ length: 90 }, () => ({ x: rnd() * 600, y: rnd() * 600, r: rnd() < 0.12 ? 1.1 : 0.6, o: 0.18 + rnd() * 0.42, d: rnd() * 6 }));
  return (
    <svg className="sky" viewBox="0 0 600 600" aria-hidden="true">
      <g className="sky-field">
        {field.map((s, i) => <circle key={i} cx={s.x} cy={s.y} r={s.r} style={{ opacity: s.o, animationDelay: `${s.d}s` }} />)}
      </g>
      <g className="sky-grid">
        <circle cx={px} cy={py} r="130" />
        <circle cx={px} cy={py} r="272" />
        <circle cx={px} cy={py} r="420" />
        {Array.from({ length: 72 }, (_, i) => {
          const a = (i * 5 * Math.PI) / 180, r1 = 420, r2 = i % 6 === 0 ? 432 : 426;
          return <line key={i} x1={px + Math.cos(a) * r1} y1={py + Math.sin(a) * r1} x2={px + Math.cos(a) * r2} y2={py + Math.sin(a) * r2} />;
        })}
      </g>
      <line className="sky-pointer" x1={mx} y1={my} x2={px} y2={py + 10} />
      <path className="sky-dipper" d={path(k, dx, dy)} />
      {(Object.keys(DIPPER) as (keyof typeof DIPPER)[]).map((n) => {
        const [x, y] = at(n);
        const hub = n === "dubhe";
        return (
          <g key={n} className={hub ? "sky-star sky-hub" : "sky-star"}>
            {hub && <circle className="sky-halo" cx={x} cy={y} r="22" />}
            <circle cx={x} cy={y} r={hub ? 6 : 3.4} />
            <text x={x + (hub ? 14 : 9)} y={y - (hub ? 12 : 8)}>{NAMES[n]}</text>
          </g>
        );
      })}
      <g className="sky-star sky-polaris">
        <circle cx={px} cy={py} r="3.6" />
        <text x={px + 10} y={py - 8}>北极星</text>
      </g>
      <text className="sky-caption" x="24" y="576">α UMa · 赤经 11h 03m · 赤纬 +61° 45′</text>
    </svg>
  );
}
