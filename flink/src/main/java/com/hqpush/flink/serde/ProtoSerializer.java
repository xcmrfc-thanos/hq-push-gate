package com.hqpush.flink.serde;

import com.google.protobuf.Message;
import org.apache.flink.api.common.serialization.SerializationSchema;

/** Kafka protobuf 序列化。 */
public class ProtoSerializer<T extends Message> implements SerializationSchema<T> {

    @Override
    public byte[] serialize(T element) {
        return element.toByteArray();
    }
}
