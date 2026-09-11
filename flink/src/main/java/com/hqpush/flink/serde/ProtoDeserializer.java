package com.hqpush.flink.serde;

import com.google.protobuf.InvalidProtocolBufferException;
import com.google.protobuf.Message;
import com.google.protobuf.Parser;
import org.apache.flink.api.common.serialization.DeserializationSchema;
import org.apache.flink.api.common.typeinfo.TypeInformation;

import java.io.IOException;

/** Kafka protobuf 值反序列化（坏消息返回 null 跳过；tick/规则可由重放与对账兜底）。
 *
 * 序列化注意（B18 联调实测修复）：protobuf 的 Parser 匿名类（Xxx$1）不可 Java 序列化，
 * 而 Flink 会把 DeserializationSchema 整体序列化进作业图——parser 必须 transient，
 * 运行时按消息类型的静态 PARSER 字段懒加载重建。 */
public class ProtoDeserializer<T extends Message> implements DeserializationSchema<T> {

    private final Class<T> type;
    private transient volatile Parser<T> parser;

    public ProtoDeserializer(Parser<T> parser, Class<T> type) {
        this.type = type;
        this.parser = parser;
    }

    private Parser<T> parser() {
        Parser<T> p = this.parser;
        if (p == null) {
            synchronized (this) {
                p = this.parser;
                if (p == null) {
                    try {
                        // 新 protoc 生成的消息类没有静态 PARSER 字段；getDefaultInstance()
                        // 是 protobuf 保证存在的 public static，getParserForType() 走 Message 接口。
                        @SuppressWarnings("unchecked")
                        T inst = (T) type.getMethod("getDefaultInstance").invoke(null);
                        @SuppressWarnings("unchecked")
                        Parser<T> parsed = (Parser<T>) inst.getParserForType();
                        this.parser = p = parsed;
                    } catch (ReflectiveOperationException e) {
                        throw new IllegalStateException("cannot obtain parser for " + type.getName(), e);
                    }
                }
            }
        }
        return p;
    }

    @Override
    public T deserialize(byte[] message) throws IOException {
        try {
            return parser().parseFrom(message);
        } catch (InvalidProtocolBufferException e) {
            return null;
        }
    }

    @Override
    public TypeInformation<T> getProducedType() {
        return TypeInformation.of(type);
    }

    @Override
    public boolean isEndOfStream(T nextElement) {
        return false;
    }
}
