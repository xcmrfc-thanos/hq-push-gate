import { Button, Empty, List, Tag } from "antd-mobile";
import { useState } from "react";
import { apiClient } from "../services/socket";
import { useAlertStore } from "../stores/stores";

/** H5 告警中心：与 pc 同数据面（补拉 + WS 推送）；支持客户端 ACK。 */
export function AlertsPage() {
  const items = useAlertStore((s) => s.items);
  const markAcked = useAlertStore((s) => s.markAcked);
  const [acking, setAcking] = useState<string | null>(null);

  const ack = async (deliveryId: string) => {
    setAcking(deliveryId);
    try {
      await apiClient.ackAlert(deliveryId);
      markAcked([deliveryId]);
    } catch {
      // ACK 失败不影响展示；服务端状态由重试/补拉兜底
    } finally {
      setAcking(null);
    }
  };

  const pending = items.filter((a) => !a.acked).length;

  return (
    <List
      header={`告警中心（72h 窗口内可补拉）${pending > 0 ? ` · ${pending} 条待确认` : ""}`}
    >
      {items.length === 0 && <Empty style={{ padding: 48 }} description="暂无告警" />}
      {items.map((a) => (
        <List.Item
          key={a.deliveryId}
          description={
            <>
              {a.detail || "—"}
              <br />
              {new Date(a.ts).toLocaleString()}
            </>
          }
          extra={
            a.acked ? (
              <Tag color="success" fill="outline">已确认</Tag>
            ) : (
              <Button size="mini" color="primary" fill="outline" loading={acking === a.deliveryId}
                onClick={() => void ack(a.deliveryId)}>
                确认
              </Button>
            )
          }
        >
          {a.symbol} · {a.title}
        </List.Item>
      ))}
    </List>
  );
}
