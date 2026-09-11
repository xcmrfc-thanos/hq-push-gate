import type { AlertItem } from "../stores/alertStore";
import { useAlertStore } from "../stores/alertStore";
import { List, Card, Tag, Typography, Button, Empty } from "antd";
import { useNavigate } from "react-router-dom";
import { apiClient } from "../services/socket";

export function AlertsPage() {
  const items = useAlertStore((s) => s.items);
  const markAcked = useAlertStore((s) => s.markAcked);
  const nav = useNavigate();

  const ack = async (a: AlertItem) => {
    try {
      await apiClient.ackAlert(a.deliveryId);
      markAcked([a.deliveryId]);
    } catch {
      // ACK 失败不影响展示；服务端状态由重试/补拉兜底
    }
  };

  return (
    <Card title="告警中心" styles={{ body: { maxHeight: "70vh", overflow: "auto" } }}>
      <List
        locale={{
          emptyText: (
            <Empty
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description="暂无告警"
              style={{ padding: 32 }}
            >
              <Button type="primary" onClick={() => nav("/rules")}>去配置预警规则</Button>
              <Typography.Paragraph type="secondary" style={{ marginTop: 12, fontSize: 12 }}>
                规则命中后实时推送；断线期间的服务端投递在 72h 窗口内可自动补拉。
              </Typography.Paragraph>
            </Empty>
          ),
        }}
        dataSource={items}
        renderItem={(a) => (
          <List.Item
            actions={
              a.acked
                ? [<Tag key="ok" color="green">已确认</Tag>]
                : [<Button key="ack" size="small" onClick={() => ack(a)}>确认</Button>]
            }
          >
            <List.Item.Meta
              title={
                <Typography.Text strong>
                  {a.symbol} · {a.title}{" "}
                  {a.acked ? null : <Tag color="red">待确认</Tag>}
                </Typography.Text>
              }
              description={
                <>
                  <div>{a.detail || "—"}</div>
                  <Typography.Text type="secondary">
                    {new Date(a.ts).toLocaleString()} · deliveryId={a.deliveryId}
                  </Typography.Text>
                </>
              }
            />
          </List.Item>
        )}
      />
    </Card>
  );
}
