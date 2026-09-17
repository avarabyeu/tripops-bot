import { api } from "../api";
import { dateRange } from "../format";
import { useNavigation, useParam } from "../router";
import { useAsync } from "../useAsync";
import { Loaded, Screen } from "../components/Screen";
import { AsyncButton, Button, Card } from "../components/ui";
import { haptic } from "../telegram";

/**
 * What somebody sees when they open an invite link: enough about the trip to
 * decide, and one button.
 */
export function Join() {
  const token = useParam("token");
  const nav = useNavigation();
  const preview = useAsync(() => api.invites.preview(token), [token]);

  return (
    <Screen title="You are invited">
      <Loaded state={preview} skeletonRows={2}>
        {(data) => (
          <>
            <Card>
              <div className="title" style={{ fontSize: 20 }}>
                {data.trip.title}
              </div>
              <div className="muted">{dateRange(data.trip.start_date, data.trip.end_date)}</div>
              {data.trip.description && <div className="muted">{data.trip.description}</div>}
              <div className="divider" />
              <div className="card-row">
                <span className="muted">Organiser</span>
                <span>{data.owner_name}</span>
              </div>
              <div className="card-row">
                <span className="muted">Going</span>
                <span>{data.member_count}</span>
              </div>
            </Card>

            {data.already_joined ? (
              <Button
                block
                onClick={() => nav.reset({ name: "trip", params: { tripId: data.trip.id } })}
              >
                Open trip
              </Button>
            ) : (
              <AsyncButton
                block
                onClick={async () => {
                  const result = await api.invites.join(token);
                  haptic.success();
                  nav.reset({ name: "trip", params: { tripId: result.trip.id } });
                }}
              >
                ✅ Join this trip
              </AsyncButton>
            )}
            <Button variant="ghost" block onClick={() => nav.reset({ name: "trips" })}>
              Not now
            </Button>
          </>
        )}
      </Loaded>
    </Screen>
  );
}
