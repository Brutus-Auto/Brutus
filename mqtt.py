#!/usr/bin/python3
import paho.mqtt.client as mqtt
import logging
import json
import re
import time
import os
import threading


conf_path = '/usr/Brutus/conf.json'
broker_address = "localhost"
fifo_path = '/usr/Brutus/test_pipe'
topics_path = '/usr/Brutus/topics.txt'
logs_path = '/usr/Brutus/mqtt_logs.log'

with open(conf_path, 'r') as file:
    config = json.load(file)


logging.basicConfig(filename=logs_path, level=logging.INFO,
                    format='%(asctime)s - %(message)s')


def on_connect(client, userdata, flags, rc, properties=None):
    logging.info("Connected with result code " + str(rc))

    with open(topics_path, 'r') as file:
        topics = file.readlines()
    

    for topic in topics:
        topic = topic.strip()
        client.subscribe(topic)

    fifo_thread_instance = threading.Thread(target=fifo_thread, daemon=True)
    fifo_thread_instance.start()


def on_disconnect(client, userdata, rc):
    logging.info("Connection is lost with the code " + str(rc))


def on_message(client, userdata, message):
    payload = message.payload.decode()
    topic = message.topic
    log_entry = f"{topic} {payload}"

    if payload:
        logging.info(log_entry)

        device, control, value = extract_topic_and_value(topic, payload)
    
        if device and control:
            if device in ("A1", "A2", "A3", "A4"):
                port = "RS-485-2"
            elif device in ("Climate_Living_room", "Warm_floor_WC_1"):
                port = "RS-485-1"
            elif device in ("bedside_switch", "shutter_living_room", "Cardholder"):
                port = "Virtual"
            elif device == "wb-gpio":
                port = "System"
            else:
                port = "Unknown"

            config[port][device][control] = value
            
            with open(conf_path, 'w') as file:
                json.dump(config, file, indent=4)


def extract_topic_and_value(topic, payload):
    pattern = r"/devices/([^/]+)/controls/([^/]+)"
    match = re.match(pattern, topic)
    
    if match:
        device = match.group(1)
        control = match.group(2)
        value = payload
        return device, control, value
    else:
        return None, None, None


def fifo_thread():
    if not os.path.exists(fifo_path):
        os.mkfifo(fifo_path)
        logging.info(f"FIFO created at {fifo_path}")

    logging.info("Waiting for messages from FIFO...")

    with open(fifo_path, 'r') as fifo:
        while True:
            try:
                message = fifo.readline().strip()

                if message:
                    logging.info(f"Received message: {message}")
                    regular_match(message)
                    topic, payload = message.split(' ', 1)
                    publish_message(topic, payload)
            
            except Exception as e:
                logging.error(f"Error while reading from FIFO: {e}")
                

def publish_message(topic, payload):
    try:
        logging.info(f"Publishing message: {topic} {payload}")
        client.publish(topic, payload)
    except Exception as e:
        logging.error(f"Error while publishing message: {e}")


def regular_match(message):
    pattern = r"^([a-zA-Z0-9_]+)\/([a-zA-Z0-9_]+)\/([a-zA-Z0-9_]+) (.+)$"
    match = re.match(pattern, message)
    
    if match:
        port = match.group(1)
        device_name = match.group(2)
        control = match.group(3)
        param = match.group(4)
        return port, device_name, control, param
    else:
        return None, None, None, None


client = mqtt.Client(protocol=mqtt.MQTTv5)

client.connect(broker_address, 1883, 60)

client.on_connect = on_connect
client.on_disconnect = on_disconnect
client.on_message = on_message

client.loop_start()
time.sleep(2)

try:
    while True:
        pass

except KeyboardInterrupt:
    client.disconnect()
    time.sleep(1)
    client.loop_stop()