import { api } from "../api";
import { useParam } from "../router";
import { useAsync } from "../useAsync";
import { Loaded, Screen } from "../components/Screen";
import { AsyncButton, Avatar, Card, Empty } from "../components/ui";
import { share, alert } from "../telegram";

/**
 * Who is going, and the one button that changes that: the invite link.
 */
export function People() {
  const tripId = useParam("tripId");
  const trip = useAsync(() => api.trips.get(tripId), [tripId]);
  const members = useAsync(() => api.members.list(tripId), [tripId]);
  const canManage = trip.data ? trip.data.me.role !== "member" : false;
  const invites = useAsync(
    () => (canManage ? api.invites.list(tripId) : Promise.resolve([])),
    [tripId, canManage],
  );

  const active = members.data?.filter((m) => m.status === "active").length ?? 0;
  const link = invites.data?.[0]?.url;

  const inviteSomeone = async () => {
    const invite = link ? { url: link } : await api.invites.create(tripId);
    if (!invite.url) {
      alert("The bot username is not configured, so no link can be built.");
      return;
    }
    invites.reload();
    share(invite.url, `Join ${trip.data?.trip.title ?? "our trip"} on TripOps`);
  };

  return (
    <Screen title="People" subtitle={members.data ? `${active} going` : undefined}>
      <Loaded state={members}>
        {(list) =>
          list.length === 0 ? (
            <Empty emoji="👥" title="Nobody here yet" description="Share the invite link with your group." />
          ) : (
            <div className="stack tight">
              {list.map((member) => (
                <Card key={member.id} tight>
                  <div className="row">
                    <Avatar name={member.display_name} photo={member.photo_url} />
                    <div style={{ flex: 1 }}>
                      <div className="title">{member.display_name}</div>
                      <div className="tiny">
                        {member.role !== "member" && `${member.role} · `}
                        {member.status === "active" ? "joined" : member.status}
                        {member.username && ` · @${member.username}`}
                      </div>
                    </div>
                    {member.status !== "active" && <span title="Has not joined yet">⏳</span>}
                  </div>
                </Card>
              ))}
            </div>
          )
        }
      </Loaded>

      {canManage && (
        <div className="bottom-action">
          <AsyncButton block onClick={inviteSomeone}>
            🔗 {link ? "Share invite link" : "Create invite link"}
          </AsyncButton>
        </div>
      )}
    </Screen>
  );
}
