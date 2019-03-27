package dao

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/gogo/protobuf/proto"
	"github.com/gomodule/redigo/redis"
	"github.com/liftbridge-io/go-liftbridge"
	"github.com/nats-io/go-nats"

	"github.com/tsingson/goim/internal/nats/logic/conf"

	pb "github.com/tsingson/goim/api/logic/grpc"
)

// NatsDao dao.
type NatsDao struct {
	c           *conf.NatsConfig
	natsClient  *nats.Conn
	liftClient  liftbridge.Client
	redis       *redis.Pool
	redisExpire int32
}

type NatsConfig struct {
	Channel   string
	ChannelID string
	Group     string
	NatsAddr  string
	LiftAddr  string
}

// New new a dao and return.
func New(c *conf.NatsConfig) *NatsDao {

	conn, err := newNatsClient(c.Nats.NatsAddr, c.Nats.LiftAddr, c.Nats.Channel, c.Nats.ChannelID)
	if err != nil {
		return nil
	}

	d := &NatsDao{
		c:          c,
		natsClient: conn,
		redis:      newRedis(),
		// TODO: handler redis expire
		redisExpire: int32(time.Duration(c.Redis.Expire) / time.Second),
	}
	return d
}

// Close close the resource.
func (d *NatsDao) Close() error {
	d.natsClient.Close()
	return d.redis.Close()
}

// Ping dao ping.
func (d *NatsDao) Ping(c context.Context) error {
	return d.pingRedis(c)
}

// PushMsg push a message to databus.
func (d *NatsDao) PushMsg(c context.Context, op int32, server string, keys []string, msg []byte) (err error) {
	pushMsg := &pb.PushMsg{
		Type:      pb.PushMsg_PUSH,
		Operation: op,
		Server:    server,
		Keys:      keys,
		Msg:       msg,
	}
	b, err := proto.Marshal(pushMsg)
	if err != nil {
		return
	}

	d.publishMessage(d.c.Nats.Channel, d.c.Nats.AckInbox, []byte(keys[0]), b)
	return
}

// BroadcastRoomMsg push a message to databus.
func (d *NatsDao) BroadcastRoomMsg(c context.Context, op int32, room string, msg []byte) (err error) {
	pushMsg := &pb.PushMsg{
		Type:      pb.PushMsg_ROOM,
		Operation: op,
		Room:      room,
		Msg:       msg,
	}
	b, err := proto.Marshal(pushMsg)
	if err != nil {
		return
	}

	d.publishMessage(d.c.Nats.Channel, d.c.Nats.AckInbox, []byte(room), b)
	return
}

// BroadcastMsg push a message to databus.
func (d *NatsDao) BroadcastMsg(c context.Context, op, speed int32, msg []byte) (err error) {
	pushMsg := &pb.PushMsg{
		Type:      pb.PushMsg_BROADCAST,
		Operation: op,
		Speed:     speed,
		Msg:       msg,
	}
	b, err := proto.Marshal(pushMsg)
	if err != nil {
		return
	}

	key := strconv.FormatInt(int64(op), 10)

	d.publishMessage(d.c.Nats.Channel, d.c.Nats.AckInbox, []byte(key), b)

	return
}

func newNatsClient(natsAddr, liftAddr, channel, channelID string) (*nats.Conn, error) {
	// liftAddr := "localhost:9292" // address for lift-bridge
	// channel := "bar"
	// channelID := "bar-stream"
	// ackInbox := "acks"

	if err := createStream(liftAddr, channel, channelID); err != nil {
		if err != liftbridge.ErrStreamExists {
			return nil, err
		}
	}
	// conn, err := nats.GetDefaultOptions().Connect()
	// natsAddr := "nats://localhost:4222"
	return nats.Connect(natsAddr)

	// defer conn.Flush()
	// defer conn.Close()

}

func (d *NatsDao) publishMessage(channel, ackInbox string, key, value []byte) error {
	var wg sync.WaitGroup

	sub, err := d.natsClient.Subscribe(ackInbox, func(m *nats.Msg) {
		ack, err := liftbridge.UnmarshalAck(m.Data)
		if err != nil {
			// TODO: handel error write to log
			return
		}
		fmt.Println("ack:", ack.StreamSubject, ack.StreamName, ack.Offset, ack.MsgSubject)
		wg.Done()
	})
	if err != nil {
		return err
	}
	defer sub.Unsubscribe()

	m := liftbridge.NewMessage(value, liftbridge.MessageOptions{Key: key, AckInbox: ackInbox})

	if err := d.natsClient.Publish(channel, m); err != nil {
		return err
	}

	wg.Wait()
	return nil
}

func createStream(liftAddr, subject, name string) error {

	client, err := liftbridge.Connect([]string{liftAddr})
	if err != nil {
		return err
	}
	defer client.Close()

	stream := liftbridge.StreamInfo{
		Subject:           subject,
		Name:              name,
		ReplicationFactor: 1,
	}
	if err := client.CreateStream(context.Background(), stream); err != nil {
		if err != liftbridge.ErrStreamExists {
			return err
		}
	}

	return nil
}
