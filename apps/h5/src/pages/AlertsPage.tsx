import { Empty, List, Tag } from "antd-mobile";
import { useAlertStore } from "../stores/stores";

export function AlertsPage() {
  const items = useAlertStore((s) => s.items);

  return (
    <List header="告警中心（72h 窗口内可补拉）">
      {items.length === 0 && <Empty style={{ padding: 48 }} description="暂无告警" />}
      {items.map((a) => (
        <List.Item
          key={a.deliveryId}
          description={
            <>
              {new Date(a.ts).toLocaleString()}
              <br />
              deliveryId={a.deliveryId}
            </>
          }
          extra={<Tag color="danger">待确认</Tag>}
        >
          {a.symbol} · {a.title}
        </List.Item>
      ))}
    </List>
  );
}
