import { useEffect, useRef, useState } from "react";

/**
 * 价格单元格：价格变化时按方向闪烁背景（涨红跌绿，与 A 股习惯一致），
 * 等宽数字避免跳动。纯 CSS 动画，无重列表开销。
 */
export function PriceCell({ value, style }: { value: number; style?: React.CSSProperties }) {
  const prev = useRef(value);
  const [flash, setFlash] = useState<"" | "flash-up" | "flash-down">("");

  useEffect(() => {
    if (value > prev.current) setFlash("flash-up");
    else if (value < prev.current) setFlash("flash-down");
    prev.current = value;
    if (value !== prev.current || flash) {
      const t = setTimeout(() => setFlash(""), 650);
      return () => clearTimeout(t);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value]);

  return (
    <span
      className={`num ${flash}`}
      style={{ fontWeight: 600, padding: "2px 6px", margin: "-2px -6px", borderRadius: 6, ...style }}
    >
      {value >= 100 ? value.toFixed(2) : value.toFixed(4)}
    </span>
  );
}
